package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/canutoilberto/gomq/internal/domain"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Em produção, validar origem
	},
}

// Handler é o handler WebSocket para o protocolo de mensageria.
type Handler struct {
	broker domain.MessageBroker
}

// NewHandler cria um novo handler WebSocket.
func NewHandler(broker domain.MessageBroker) *Handler {
	return &Handler{broker: broker}
}

// ServeHTTP implementa http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}

	session := newSession(conn, h.broker)
	session.run()
}

// session representa uma sessão WebSocket.
type session struct {
	conn   *websocket.Conn
	broker domain.MessageBroker

	// subscriptions mapeia queue -> deliverCh
	subscriptions map[string]chan domain.DeliverFrame
	subsMu        sync.RWMutex

	// consumerIDs mapeia queue -> consumerID
	consumerIDs map[string]string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newSession(conn *websocket.Conn, broker domain.MessageBroker) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		conn:          conn,
		broker:        broker,
		subscriptions: make(map[string]chan domain.DeliverFrame),
		consumerIDs:   make(map[string]string),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (s *session) run() {
	defer s.cleanup()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		_, message, err := s.conn.ReadMessage()
		if err != nil {
			return
		}

		s.handleMessage(message)
	}
}

func (s *session) cleanup() {
	s.cancel()
	s.wg.Wait()

	// Unsubscribe de todas as filas
	s.subsMu.Lock()
	for queue, consumerID := range s.consumerIDs {
		_ = s.broker.Unsubscribe(context.Background(), consumerID, queue)
	}
	s.subsMu.Unlock()

	_ = s.conn.Close()
}

func (s *session) handleMessage(data []byte) {
	var generic domain.GenericFrame
	if err := json.Unmarshal(data, &generic); err != nil {
		s.sendError("", domain.WSErrInvalidPayload, "invalid JSON")
		return
	}

	switch generic.Action {
	case domain.ActionPublish:
		s.handlePublish(data)
	case domain.ActionSubscribe:
		s.handleSubscribe(data)
	case domain.ActionUnsubscribe:
		s.handleUnsubscribe(data)
	case domain.ActionAck:
		s.handleAck(data)
	case domain.ActionNack:
		s.handleNack(data)
	default:
		s.sendError("", domain.WSErrInvalidPayload, "unknown action")
	}
}

func (s *session) handlePublish(data []byte) {
	var frame domain.PublishFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		s.sendError("", domain.WSErrInvalidPayload, "invalid publish frame")
		return
	}

	ack, err := s.broker.Publish(s.ctx, frame)
	if err != nil {
		s.handleBrokerError(frame.RequestID, err)
		return
	}

	s.send(ack)
}

func (s *session) handleSubscribe(data []byte) {
	var frame domain.SubscribeFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		s.sendError("", domain.WSErrInvalidPayload, "invalid subscribe frame")
		return
	}

	// Criar channel de entrega
	deliverCh := make(chan domain.DeliverFrame, 100)

	ack, err := s.broker.Subscribe(s.ctx, frame, deliverCh)
	if err != nil {
		s.handleBrokerError("", err)
		close(deliverCh)
		return
	}

	// Registrar subscription
	s.subsMu.Lock()
	s.subscriptions[frame.Queue] = deliverCh
	s.consumerIDs[frame.Queue] = ack.ConsumerID
	s.subsMu.Unlock()

	// Iniciar goroutine para forward de mensagens
	s.wg.Add(1)
	go s.forwardDeliveries(frame.Queue, deliverCh)

	s.send(ack)
}

func (s *session) forwardDeliveries(queue string, deliverCh <-chan domain.DeliverFrame) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case delivery, ok := <-deliverCh:
			if !ok {
				return
			}
			s.send(delivery)
		}
	}
}

func (s *session) handleUnsubscribe(data []byte) {
	var frame domain.UnsubscribeFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		s.sendError("", domain.WSErrInvalidPayload, "invalid unsubscribe frame")
		return
	}

	s.subsMu.Lock()
	consumerID, exists := s.consumerIDs[frame.Queue]
	if exists {
		delete(s.subscriptions, frame.Queue)
		delete(s.consumerIDs, frame.Queue)
	}
	s.subsMu.Unlock()

	if !exists {
		s.sendError("", domain.WSErrNotSubscribed, "not subscribed to queue")
		return
	}

	_ = s.broker.Unsubscribe(s.ctx, consumerID, frame.Queue)
}

func (s *session) handleAck(data []byte) {
	var frame domain.AckFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		s.sendError("", domain.WSErrInvalidPayload, "invalid ack frame")
		return
	}

	if err := s.broker.Ack(s.ctx, frame); err != nil {
		s.handleBrokerError("", err)
	}
}

func (s *session) handleNack(data []byte) {
	var frame domain.NackFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		s.sendError("", domain.WSErrInvalidPayload, "invalid nack frame")
		return
	}

	if err := s.broker.Nack(s.ctx, frame); err != nil {
		s.handleBrokerError("", err)
	}
}

func (s *session) handleBrokerError(requestID string, err error) {
	if wsErr, ok := err.(*domain.WSDomainError); ok {
		s.sendError(requestID, wsErr.Code, wsErr.Message)
	} else {
		s.sendError(requestID, domain.WSErrInternalError, err.Error())
	}
}

func (s *session) sendError(requestID string, code domain.WSErrorCode, message string) {
	s.send(domain.ErrorFrame{
		Action:    domain.ActionError,
		RequestID: requestID,
		Code:      code,
		Message:   message,
	})
}

func (s *session) send(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = s.conn.WriteMessage(websocket.TextMessage, data)
}
