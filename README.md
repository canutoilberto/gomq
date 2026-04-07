# GoMQ

Broker de mensagens em memória, escrito em Go, com duas interfaces públicas no mesmo processo:

- API REST para gerenciamento de filas
- WebSocket para publicação, consumo e confirmação de mensagens

O projeto foi desenhado para cenários de desenvolvimento, testes, prototipação e cargas leves onde persistência em disco não é requisito.

## O que a aplicação faz

- Cria, lista, consulta e remove filas via HTTP
- Publica mensagens em filas via WebSocket
- Entrega mensagens a consumidores via WebSocket
- Exige `ack` ou `nack` por entrega
- Reentrega mensagens quando o `ack_timeout` expira
- Suporta DLQ opcional por fila
- Expira mensagens por TTL quando configurado
- Distribui entregas em round-robin entre consumidores da mesma fila

## Características da implementação atual

- Single-node
- Armazenamento totalmente em memória
- Sem autenticação ou autorização
- Sem persistência em disco
- Sem ordenação por prioridade: o campo `priority` é aceito e transportado, mas não altera a ordem de entrega
- HTTP e WebSocket compartilham a mesma porta

## Endpoints expostos

Na configuração padrão, o servidor sobe em `http://localhost:8080`.

### Health check

- `GET /health`

Resposta:

```json
{"status":"ok"}
```

### Management API

- `POST /api/v1/queues`
- `GET /api/v1/queues`
- `GET /api/v1/queues/{name}`
- `DELETE /api/v1/queues/{name}`

Exemplo de criação de fila:

```bash
curl -X POST http://localhost:8080/api/v1/queues \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "orders.created",
    "config": {
      "ttl_seconds": 60,
      "max_size": 1000,
      "enable_dlq": true,
      "max_retries": 3
    }
  }'
```

Exemplo de listagem:

```bash
curl 'http://localhost:8080/api/v1/queues?page=1&page_size=20'
```

## Protocolo WebSocket

O endpoint de mensageria fica em:

```text
ws://localhost:8080/ws
```

Frames suportados do cliente para o broker:

- `publish`
- `subscribe`
- `unsubscribe`
- `ack`
- `nack`

Frames enviados pelo broker:

- `publish_ack`
- `subscribe_ack`
- `deliver`
- `error`

### Exemplo de publicação

```json
{
  "action": "publish",
  "request_id": "req-1",
  "queue": "orders.created",
  "body": "{\"orderId\":123}",
  "headers": {
    "content-type": "application/json"
  },
  "priority": 5
}
```

### Exemplo de inscrição

```json
{
  "action": "subscribe",
  "queue": "orders.created",
  "ack_timeout_seconds": 30
}
```

### Exemplo de entrega

```json
{
  "action": "deliver",
  "delivery_id": "2d5c3c91-6a0c-49c8-a85c-0919d0ff5e88",
  "message_id": "eeb74c8c-4fe7-459a-9594-80999d679c3d",
  "queue": "orders.created",
  "body": "{\"orderId\":123}",
  "headers": {
    "content-type": "application/json"
  },
  "priority": 5,
  "attempt": 1,
  "max_attempts": 3,
  "published_at": "2026-04-07T12:00:00Z"
}
```

### Exemplo de confirmação

Ack:

```json
{
  "action": "ack",
  "delivery_id": "2d5c3c91-6a0c-49c8-a85c-0919d0ff5e88"
}
```

Nack com requeue:

```json
{
  "action": "nack",
  "delivery_id": "2d5c3c91-6a0c-49c8-a85c-0919d0ff5e88",
  "reason": "temporary failure",
  "requeue": true
}
```

## Garantias e comportamento

- Semântica de entrega: at-least-once
- Mensagens podem ser redeliveradas
- Consumidores devem ser idempotentes
- `ack_timeout_seconds` padrão: `30`
- `max_retries` padrão: `3`
- `ttl_seconds=0` desabilita expiração
- `max_size=0` usa um buffer grande em memória
- Quando `enable_dlq=true`, a DLQ é criada automaticamente como `{fila}.dlq`

Headers adicionados quando uma mensagem vai para DLQ:

- `x-dlq-reason`
- `x-dlq-original-queue`
- `x-dlq-moved-at`
- `x-dlq-total-attempts`
- `x-dlq-last-nack-reason`

## Configuração

Variáveis de ambiente suportadas:

- `PORT`: porta do servidor; tem precedência em plataformas como Render
- `GOMQ_HTTP_PORT`: porta do servidor quando `PORT` não estiver definido
- `GOMQ_SHUTDOWN_TIMEOUT`: timeout de shutdown gracioso em segundos

Defaults:

- `PORT`/`GOMQ_HTTP_PORT`: `8080`
- `GOMQ_SHUTDOWN_TIMEOUT`: `10`

## Execução local

Pré-requisito: Go `1.25`.

```bash
go run ./cmd/gomq
```

Ou compilando o binário:

```bash
go build -o bin/gomq ./cmd/gomq
./bin/gomq
```

## Docker

Build:

```bash
docker build -t gomq .
```

Run:

```bash
docker run --rm -p 8080:8080 gomq
```

## Deploy no Render

O repositório já inclui [`render.yaml`](/home/ilberto/dev/gomq/render.yaml) para deploy como serviço web com Docker e health check em `/health`.

Observação operacional:

- O código prioriza `PORT` sobre `GOMQ_HTTP_PORT`
- Em provedores que injetam `PORT`, esse será o valor usado pelo processo

## Testes

Executar toda a suíte:

```bash
go test ./...
```

A cobertura atual exercita:

- validações de domínio
- CRUD de filas
- cenários concorrentes de publish/subscribe/ack/nack
- TTL e fluxo de DLQ

## Estrutura do projeto

```text
cmd/gomq              Entrypoint da aplicação
internal/broker       Engine do broker, dispatcher, TTL e retries
internal/config       Leitura de configuração por ambiente
internal/domain       DTOs, contratos e validações
internal/transport    HTTP REST e WebSocket
docs/                 OpenAPI, AsyncAPI e arquitetura
```

## Limitações importantes

- O broker não persiste mensagens; reiniciar o processo limpa o estado
- Não há clustering, replicação ou consenso
- Não há autenticação, autorização ou rate limiting
- O `CheckOrigin` do WebSocket está permissivo na implementação atual
- A documentação em [`docs/architecture.md`](/home/ilberto/dev/gomq/docs/architecture.md) menciona portas HTTP e WebSocket separadas, mas o binário atual expõe ambos na mesma porta

## Documentação complementar

- [`docs/architecture.md`](/home/ilberto/dev/gomq/docs/architecture.md)
- [`docs/management-api.yaml`](/home/ilberto/dev/gomq/docs/management-api.yaml)
- [`docs/messaging-protocol.yaml`](/home/ilberto/dev/gomq/docs/messaging-protocol.yaml)
