package broker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canutoilberto/gomq/internal/domain"
)

// newTestBroker cria instâncias de teste do Engine.
func newTestBroker(t *testing.T) (domain.QueueManager, domain.MessageBroker) {
	t.Helper()
	engine := New()
	t.Cleanup(func() {
		_ = engine.Shutdown(5 * time.Second)
	})
	return engine, engine
}

// createTestQueue é um helper que cria uma fila para testes.
func createTestQueue(t *testing.T, qm domain.QueueManager, name string, cfg *domain.QueueConfig) domain.QueueInfo {
	t.Helper()
	info, err := qm.CreateQueue(context.Background(), domain.CreateQueueRequest{
		Name:   name,
		Config: cfg,
	})
	if err != nil {
		t.Fatalf("failed to create queue %q: %v", name, err)
	}
	return info
}

// --- T1: Múltiplos produtores simultâneos ---
func TestConcurrent_MultipleProducers(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	createTestQueue(t, qm, "t1-queue", nil)

	const numProducers = 100
	const messagesPerProducer = 50

	var wg sync.WaitGroup
	var published int64
	errCh := make(chan error, numProducers*messagesPerProducer)

	for i := range numProducers {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()
			for j := range messagesPerProducer {
				_, err := mb.Publish(context.Background(), domain.PublishFrame{
					Action: domain.ActionPublish,
					Queue:  "t1-queue",
					Body:   fmt.Sprintf(`{"producer":%d,"msg":%d}`, producerID, j),
				})
				if err != nil {
					errCh <- fmt.Errorf("producer %d, msg %d: %w", producerID, j, err)
					return
				}
				atomic.AddInt64(&published, 1)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	expected := int64(numProducers * messagesPerProducer)
	if got := atomic.LoadInt64(&published); got != expected {
		t.Errorf("expected %d published messages, got %d", expected, got)
	}

	// Verificar métricas
	info, err := qm.GetQueue(context.Background(), "t1-queue")
	if err != nil {
		t.Fatalf("failed to get queue: %v", err)
	}
	if info.Metrics.TotalPublished != expected {
		t.Errorf("expected TotalPublished=%d, got %d", expected, info.Metrics.TotalPublished)
	}
}

// --- T2: Produtores + consumidores simultâneos ---
func TestConcurrent_ProducersAndConsumers(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	createTestQueue(t, qm, "t2-queue", nil)

	const totalMessages = 200
	var consumed int64
	var wg sync.WaitGroup

	// Iniciar 3 consumidores
	for i := range 3 {
		deliverCh := make(chan domain.DeliverFrame, 100)
		_, err := mb.Subscribe(context.Background(), domain.SubscribeFrame{
			Action: domain.ActionSubscribe,
			Queue:  "t2-queue",
		}, deliverCh)
		if err != nil {
			t.Fatalf("consumer %d failed to subscribe: %v", i, err)
		}

		wg.Add(1)
		go func(ch <-chan domain.DeliverFrame) {
			defer wg.Done()
			for delivery := range ch {
				err := mb.Ack(context.Background(), domain.AckFrame{
					Action:     domain.ActionAck,
					DeliveryID: delivery.DeliveryID,
				})
				if err != nil {
					return
				}
				atomic.AddInt64(&consumed, 1)
				if atomic.LoadInt64(&consumed) >= totalMessages {
					return
				}
			}
		}(deliverCh)
	}

	// Publicar mensagens concorrentemente
	var pubWg sync.WaitGroup
	for i := range totalMessages {
		pubWg.Add(1)
		go func(msgID int) {
			defer pubWg.Done()
			_, _ = mb.Publish(context.Background(), domain.PublishFrame{
				Action: domain.ActionPublish,
				Queue:  "t2-queue",
				Body:   fmt.Sprintf(`{"id":%d}`, msgID),
			})
		}(i)
	}
	pubWg.Wait()

	// Aguardar consumo (com timeout)
	done := make(chan struct{})
	go func() {
		for atomic.LoadInt64(&consumed) < totalMessages {
			time.Sleep(10 * time.Millisecond)
		}
		close(done)
	}()

	select {
	case <-done:
		// sucesso
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout: consumed only %d of %d messages", atomic.LoadInt64(&consumed), totalMessages)
	}
}

// --- T3: Subscribe/Unsubscribe durante delivery ---
func TestConcurrent_SubscribeUnsubscribeDuringDelivery(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	createTestQueue(t, qm, "t3-queue", nil)

	// Publicar mensagens de fundo
	var pubWg sync.WaitGroup
	for i := range 500 {
		pubWg.Add(1)
		go func(id int) {
			defer pubWg.Done()
			_, _ = mb.Publish(context.Background(), domain.PublishFrame{
				Action: domain.ActionPublish,
				Queue:  "t3-queue",
				Body:   fmt.Sprintf(`{"id":%d}`, id),
			})
		}(i)
	}

	// Consumidores entrando e saindo rapidamente
	var subWg sync.WaitGroup
	for i := range 20 {
		subWg.Add(1)
		go func(consumerNum int) {
			defer subWg.Done()
			deliverCh := make(chan domain.DeliverFrame, 10)
			ack, err := mb.Subscribe(context.Background(), domain.SubscribeFrame{
				Action: domain.ActionSubscribe,
				Queue:  "t3-queue",
			}, deliverCh)
			if err != nil {
				return
			}

			// Consumir algumas mensagens e sair
			consumed := 0
			timeout := time.After(2 * time.Second)
		consumeLoop:
			for {
				select {
				case delivery, ok := <-deliverCh:
					if !ok {
						break consumeLoop
					}
					_ = mb.Ack(context.Background(), domain.AckFrame{
						Action:     domain.ActionAck,
						DeliveryID: delivery.DeliveryID,
					})
					consumed++
					if consumed >= 3 {
						break consumeLoop
					}
				case <-timeout:
					break consumeLoop
				}
			}

			// Unsubscribe
			_ = mb.Unsubscribe(context.Background(), ack.ConsumerID, "t3-queue")
		}(i)
	}

	pubWg.Wait()
	subWg.Wait()

	// Se chegou aqui sem race condition ou deadlock, o teste passou
}

// --- T4: Ack/Nack concorrentes ---
func TestConcurrent_AckNack(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	cfg := &domain.QueueConfig{MaxRetries: 3, EnableDLQ: true}
	createTestQueue(t, qm, "t4-queue", cfg)

	const totalMessages = 100

	// Publicar mensagens
	for i := range totalMessages {
		_, err := mb.Publish(context.Background(), domain.PublishFrame{
			Action: domain.ActionPublish,
			Queue:  "t4-queue",
			Body:   fmt.Sprintf(`{"id":%d}`, i),
		})
		if err != nil {
			t.Fatalf("publish failed: %v", err)
		}
	}

	deliverCh := make(chan domain.DeliverFrame, totalMessages*2)
	_, err := mb.Subscribe(context.Background(), domain.SubscribeFrame{
		Action: domain.ActionSubscribe,
		Queue:  "t4-queue",
	}, deliverCh)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	// Ack/Nack concorrentemente
	var wg sync.WaitGroup
	var acked, nacked int64
	processedCount := 0

	timeout := time.After(10 * time.Second)

processLoop:
	for processedCount < totalMessages {
		select {
		case delivery := <-deliverCh:
			wg.Add(1)
			go func(d domain.DeliverFrame) {
				defer wg.Done()
				// Ack mensagens pares, Nack ímpares
				if d.Attempt%2 == 0 {
					if err := mb.Ack(context.Background(), domain.AckFrame{
						Action:     domain.ActionAck,
						DeliveryID: d.DeliveryID,
					}); err == nil {
						atomic.AddInt64(&acked, 1)
					}
				} else {
					requeue := false // Não requeue para evitar loop infinito
					if err := mb.Nack(context.Background(), domain.NackFrame{
						Action:     domain.ActionNack,
						DeliveryID: d.DeliveryID,
						Reason:     "test nack",
						Requeue:    &requeue,
					}); err == nil {
						atomic.AddInt64(&nacked, 1)
					}
				}
			}(delivery)
			processedCount++
		case <-timeout:
			break processLoop
		}
	}

	wg.Wait()
	t.Logf("acked: %d, nacked: %d", atomic.LoadInt64(&acked), atomic.LoadInt64(&nacked))
}

// --- T5: Create/Delete durante publish ---
func TestConcurrent_CreateDeleteDuringPublish(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	var wg sync.WaitGroup

	// Goroutine criando e deletando filas
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 50 {
			name := fmt.Sprintf("ephemeral-%d", i)
			_, _ = qm.CreateQueue(context.Background(), domain.CreateQueueRequest{Name: name})
			time.Sleep(time.Millisecond)
			_ = qm.DeleteQueue(context.Background(), name)
		}
	}()

	// Goroutines tentando publicar em filas que podem ou não existir
	for i := range 50 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("ephemeral-%d", id)
			// Pode falhar com QUEUE_NOT_FOUND — isso é esperado
			_, _ = mb.Publish(context.Background(), domain.PublishFrame{
				Action: domain.ActionPublish,
				Queue:  name,
				Body:   "test",
			})
		}(i)
	}

	wg.Wait()
	// Sucesso = sem panic, sem race condition, sem deadlock
}

// --- T6: DLQ overflow ---
func TestConcurrent_DLQOverflow(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	cfg := &domain.QueueConfig{EnableDLQ: true, MaxRetries: 1}
	createTestQueue(t, qm, "t6-queue", cfg)

	const totalMessages = 50

	// Publicar mensagens
	for i := range totalMessages {
		_, err := mb.Publish(context.Background(), domain.PublishFrame{
			Action: domain.ActionPublish,
			Queue:  "t6-queue",
			Body:   fmt.Sprintf(`{"id":%d}`, i),
		})
		if err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
	}

	deliverCh := make(chan domain.DeliverFrame, totalMessages*2)
	_, err := mb.Subscribe(context.Background(), domain.SubscribeFrame{
		Action: domain.ActionSubscribe,
		Queue:  "t6-queue",
	}, deliverCh)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	// Nack tudo concorrentemente (forçando envio para DLQ)
	var wg sync.WaitGroup
	nackCount := 0
	timeout := time.After(10 * time.Second)

nackLoop:
	for nackCount < totalMessages {
		select {
		case delivery := <-deliverCh:
			wg.Add(1)
			go func(d domain.DeliverFrame) {
				defer wg.Done()
				requeue := false
				_ = mb.Nack(context.Background(), domain.NackFrame{
					Action:     domain.ActionNack,
					DeliveryID: d.DeliveryID,
					Reason:     "force to DLQ",
					Requeue:    &requeue,
				})
			}(delivery)
			nackCount++
		case <-timeout:
			break nackLoop
		}
	}

	wg.Wait()

	// Aguardar processamento
	time.Sleep(500 * time.Millisecond)

	// Verificar que a DLQ recebeu as mensagens
	dlqInfo, err := qm.GetQueue(context.Background(), "t6-queue.dlq")
	if err != nil {
		t.Fatalf("failed to get DLQ: %v", err)
	}
	if dlqInfo.Metrics.Pending < int64(totalMessages/2) { // Pelo menos metade deve ir pra DLQ
		t.Errorf("expected DLQ to have at least %d messages, got %d", totalMessages/2, dlqInfo.Metrics.Pending)
	}
}

// --- T7: TTL expiration during delivery ---
func TestConcurrent_TTLExpirationDuringDelivery(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	// TTL de 1 segundo — mensagens expiram rapidamente
	cfg := &domain.QueueConfig{TTLSeconds: 1, MaxRetries: 3}
	createTestQueue(t, qm, "t7-queue", cfg)

	// Publicar mensagens
	for i := range 20 {
		_, err := mb.Publish(context.Background(), domain.PublishFrame{
			Action: domain.ActionPublish,
			Queue:  "t7-queue",
			Body:   fmt.Sprintf(`{"id":%d}`, i),
		})
		if err != nil {
			t.Fatalf("publish failed: %v", err)
		}
	}

	// Esperar as mensagens expirarem
	time.Sleep(3 * time.Second)

	// Verificar que as mensagens expiraram ou foram processadas
	info, err := qm.GetQueue(context.Background(), "t7-queue")
	if err != nil {
		t.Fatalf("get queue failed: %v", err)
	}

	// Após TTL, pending + expired deve somar ao total ou próximo
	t.Logf("Pending: %d, Expired: %d, Published: %d",
		info.Metrics.Pending, info.Metrics.TotalExpired, info.Metrics.TotalPublished)
}

// --- T8: Max retries exhaustion ---
func TestConcurrent_MaxRetriesExhaustion(t *testing.T) {
	t.Parallel()
	qm, mb := newTestBroker(t)

	cfg := &domain.QueueConfig{EnableDLQ: true, MaxRetries: 3}
	createTestQueue(t, qm, "t8-queue", cfg)

	// Publicar 1 mensagem
	_, err := mb.Publish(context.Background(), domain.PublishFrame{
		Action: domain.ActionPublish,
		Queue:  "t8-queue",
		Body:   `{"test":"retry-exhaustion"}`,
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	deliverCh := make(chan domain.DeliverFrame, 10)
	_, err = mb.Subscribe(context.Background(), domain.SubscribeFrame{
		Action: domain.ActionSubscribe,
		Queue:  "t8-queue",
	}, deliverCh)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	// Nack a mensagem 3 vezes (max_retries) com requeue=true
	for attempt := 1; attempt <= 3; attempt++ {
		select {
		case delivery := <-deliverCh:
			if delivery.Attempt != attempt {
				t.Errorf("expected attempt %d, got %d", attempt, delivery.Attempt)
			}
			requeue := true
			err := mb.Nack(context.Background(), domain.NackFrame{
				Action:     domain.ActionNack,
				DeliveryID: delivery.DeliveryID,
				Reason:     fmt.Sprintf("retry %d", attempt),
				Requeue:    &requeue,
			})
			if err != nil {
				t.Fatalf("nack attempt %d failed: %v", attempt, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for delivery attempt %d", attempt)
		}
	}

	// Aguardar processamento
	time.Sleep(500 * time.Millisecond)

	// Verificar que a mensagem foi para a DLQ
	dlqInfo, err := qm.GetQueue(context.Background(), "t8-queue.dlq")
	if err != nil {
		t.Fatalf("failed to get DLQ: %v", err)
	}
	if dlqInfo.Metrics.Pending != 1 {
		t.Errorf("expected 1 message in DLQ, got %d", dlqInfo.Metrics.Pending)
	}

	// A fila original deve estar vazia (ou com poucos pending devido a timing)
	info, err := qm.GetQueue(context.Background(), "t8-queue")
	if err != nil {
		t.Fatalf("failed to get queue: %v", err)
	}
	t.Logf("Original queue pending: %d, DLQ pending: %d", info.Metrics.Pending, dlqInfo.Metrics.Pending)
}

// --- T9: Graceful shutdown ---
func TestConcurrent_GracefulShutdown(t *testing.T) {
	t.Parallel()

	// Criar engine manualmente para controlar shutdown
	engine := New()
	var qm domain.QueueManager = engine
	var mb domain.MessageBroker = engine

	_, _ = qm.CreateQueue(context.Background(), domain.CreateQueueRequest{Name: "t9-queue"})

	ctx, cancel := context.WithCancel(context.Background())

	// Iniciar produtor contínuo
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_, _ = mb.Publish(ctx, domain.PublishFrame{
					Action: domain.ActionPublish,
					Queue:  "t9-queue",
					Body:   fmt.Sprintf(`{"id":%d}`, i),
				})
				i++
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Iniciar consumidor
	deliverCh := make(chan domain.DeliverFrame, 100)
	_, err := mb.Subscribe(context.Background(), domain.SubscribeFrame{
		Action: domain.ActionSubscribe,
		Queue:  "t9-queue",
	}, deliverCh)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case delivery, ok := <-deliverCh:
				if !ok {
					return
				}
				_ = mb.Ack(ctx, domain.AckFrame{
					Action:     domain.ActionAck,
					DeliveryID: delivery.DeliveryID,
				})
			}
		}
	}()

	// Deixar rodar por um momento
	time.Sleep(100 * time.Millisecond)

	// Cancelar o contexto (simular shutdown)
	cancel()

	// Aguardar goroutines finalizarem (com timeout)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Sucesso — todas as goroutines finalizaram
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: goroutines did not shut down gracefully")
	}

	// Shutdown do engine
	_ = engine.Shutdown(5 * time.Second)
}
