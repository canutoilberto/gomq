# GoMQ — Documento de Arquitetura

## 1. Visão Geral

O **GoMQ** é um message broker leve, in-memory, escrito em Go. Ele oferece:

- **API REST** (HTTP) para gerenciamento de filas (CRUD + métricas)
- **Protocolo WebSocket** para operações de mensageria (publish, subscribe, ack/nack)
- **Garantia at-least-once** com Dead-Letter Queue (DLQ) integrada

O sistema é single-node (não distribuído) e projetado para cenários de
desenvolvimento, testes e workloads leves de produção onde persistência em disco
não é requisito.

---

## 2. Estrutura de Pacotes
gomq/
├── cmd/
│ └── gomq/
│ └── main.go # Entrypoint — inicializa servidores HTTP e WS
├── docs/
│ ├── management-api.yaml # Especificação OpenAPI 3.0 (REST)
│ ├── messaging-protocol.yaml # Especificação AsyncAPI 3.0 (WebSocket)
│ └── architecture.md # Este documento
├── internal/
│ ├── domain/ # DTOs, structs e interfaces (contratos puros)
│ │ ├── message.go
│ │ ├── queue.go
│ │ └── interfaces.go
│ ├── broker/ # Implementação do motor de mensageria
│ │ ├── engine.go # MessageBroker — gerencia publish/subscribe/ack/nack
│ │ ├── dispatcher.go # Lógica de round-robin e entrega
│ │ ├── dlq.go # Lógica de Dead-Letter Queue
│ │ └── ttl.go # Goroutine de expiração por TTL
│ ├── queue/ # Implementação do gerenciador de filas
│ │ └── manager.go # QueueManager — CRUD de filas
│ ├── transport/
│ │ ├── http/ # Handlers REST (management API)
│ │ │ ├── router.go
│ │ │ └── handlers.go
│ │ └── ws/ # Handler WebSocket (messaging protocol)
│ │ ├── handler.go
│ │ └── session.go # Representa uma conexão WS de um cliente
│ └── config/
│ └── config.go # Configuração do servidor (portas, defaults)
├── go.mod
└── go.sum

text


---

## 3. Modelo de Dados In-Memory

### 3.1 Estruturas Principais
┌─────────────────────────────────────────────────┐
│ Broker Engine │
│ │
│ ┌───────────────────────────────────────────┐ │
│ │ queues map[string]*Queue │ │
│ │ (protegido por RWMutex) │ │
│ └──────────────────┬────────────────────────┘ │
│ │ │
│ ┌───────────┴───────────┐ │
│ ▼ ▼ │
│ ┌─────────────┐ ┌─────────────┐ │
│ │ Queue "A" │ │ Queue "B" │ │
│ │ │ │ │ │
│ │ messages │ │ messages │ │
│ │ (channel) │ │ (channel) │ │
│ │ │ │ │ │
│ │ consumers │ │ consumers │ │
│ │ (slice) │ │ (slice) │ │
│ │ │ │ │ │
│ │ inFlight │ │ inFlight │ │
│ │ (map) │ │ (map) │ │
│ │ │ │ │ │
│ │ dlq ──────────┐ │ dlq (nil) │ │
│ └─────────────┘ │ └─────────────┘ │
│ ▼ │
│ ┌─────────────┐ │
│ │ DLQ "A.dlq"│ │
│ │ messages │ │
│ └─────────────┘ │
└─────────────────────────────────────────────────┘

text


### 3.2 Ciclo de Vida de uma Mensagem
text

              ┌──────────┐
              │ PUBLISHED│  (Produtor envia publish)
              └────┬─────┘
                   │
                   ▼
              ┌──────────┐
          ┌───│ PENDING  │  (Aguardando na fila)
          │   └────┬─────┘
          │        │
 TTL      │        │ Consumidor disponível
expirado │ ▼
│ ┌──────────┐
│ │IN_FLIGHT │ (Entregue, aguardando Ack/Nack)
│ └────┬─────┘
│ │
│ ┌────┴────────────┐
│ │ │
│ ▼ ▼
│ ┌─────┐ ┌──────────┐
│ │ ACK │ │ NACK │
│ └──┬──┘ └────┬─────┘
│ │ │
│ ▼ ▼
│ ┌────────┐ ┌──────────────┐
│ │CONSUMED│ │retries < max?│
│ │(removed)│ └──────┬───────┘
│ └────────┘ │ │
│ SIM NÃO
│ │ │
│ ▼ ▼
│ ┌────────┐ ┌────────────┐
│ │REQUEUED│ │ DLQ / DROP │
│ │→PENDING│ └────────────┘
│ └────────┘
│
▼
┌─────────┐
│ EXPIRED │
│(dropped)│
└─────────┘

text


---

## 4. Concorrência Segura

### 4.1 Estratégia Geral

O GoMQ usa uma **abordagem híbrida** de concorrência, escolhendo a primitiva
mais adequada para cada cenário:

| Recurso | Primitiva | Justificativa |
|---------|-----------|---------------|
| Mapa global de filas (`queues`) | `sync.RWMutex` | Leituras frequentes (list/get) vs. escritas raras (create/delete). RWMutex permite leituras concorrentes. |
| Fila de mensagens pendentes | `chan *Message` (buffered) | Go channels são naturalmente thread-safe e modelam perfeitamente o padrão FIFO produtor→consumidor. O buffer size é o `max_size` da fila. |
| Mapa in-flight | `sync.Mutex` | Escritas frequentes (cada deliver/ack/nack). Não precisa de RW pois a proporção leitura/escrita é balanceada. |
| Lista de consumidores | `sync.RWMutex` | Leitura frequente (dispatch round-robin) vs. escrita rara (subscribe/unsubscribe). |
| Métricas (contadores) | `sync/atomic` | Operações atômicas de incremento são lock-free e ideais para contadores de alta frequência. |

### 4.2 Invariantes de Concorrência

1. **Nenhuma goroutine acessa o mapa de filas sem segurar o lock apropriado.**
   - `RLock()` para `listQueues`, `getQueue`, `publish`, `subscribe`
   - `Lock()` para `createQueue`, `deleteQueue`

2. **O channel da fila é criado uma única vez no momento da criação da fila**
   e nunca é redimensionado. Isso evita race conditions na referência ao channel.

3. **Mensagens in-flight são rastreadas por `delivery_id`** (não por `message_id`),
   permitindo que a mesma mensagem tenha múltiplas entregas sem conflito de chave.

4. **A goroutine de TTL** opera em uma goroutine dedicada por fila, fazendo
   varredura periódica. Ela adquire o mutex da fila apenas brevemente para
   remover mensagens expiradas.

### 4.3 Proteção contra Deadlocks

- **Ordem de aquisição de locks:** Quando múltiplos locks são necessários,
  a ordem é sempre: `global queues RWMutex` → `queue-level Mutex` → `inFlight Mutex`.
  Nunca na ordem inversa.
- **Timeouts em channel operations:** Todas as operações de envio para channels
  usam `select` com `default` ou `time.After` para evitar bloqueio indefinido.

---

## 5. Garantia de Entrega e Dead-Letter Queue (DLQ)

### 5.1 Semântica At-Least-Once

O GoMQ garante **at-least-once delivery** através do seguinte mecanismo:

1. Quando o broker entrega uma mensagem a um consumidor (frame `deliver`),
   a mensagem é **movida** do channel para o mapa `inFlight`.
2. Um timer é iniciado com duração `ack_timeout_seconds` (padrão: 30s).
3. Se o consumidor enviar `ack` antes do timeout → mensagem removida de `inFlight`.
4. Se o consumidor enviar `nack` ou o timeout expirar → lógica de retry é acionada.

**Nota:** At-least-once significa que duplicatas são possíveis. Consumidores
devem implementar idempotência quando necessário.

### 5.2 Lógica de Retry e DLQ
Mensagem recebe Nack (ou timeout):
│
├─ requeue == false?
│ ├─ DLQ habilitada? → move para DLQ
│ └─ DLQ desabilitada? → descarta (log warning)
│
└─ requeue == true?
├─ attempt < max_retries?
│ └─ Incrementa attempt, recoloca no channel (PENDING)
│
└─ attempt >= max_retries?
├─ DLQ habilitada? → move para DLQ
└─ DLQ desabilitada? → descarta (log warning)

text


### 5.3 Estrutura da DLQ

- A DLQ é implementada como **uma fila regular** com nome `{queue_name}.dlq`.
- Ela é criada automaticamente quando `enable_dlq: true` e a fila principal
  é criada.
- A DLQ **não tem DLQ própria** (evita recursão infinita).
- A DLQ **não tem TTL** — mensagens ficam lá até serem consumidas
  manualmente ou a fila ser deletada.
- Mensagens na DLQ mantêm todos os metadados originais (headers, attempt count,
  motivo do último nack).
- A DLQ pode ser consumida normalmente via `subscribe` no protocolo WebSocket,
  permitindo reprocessamento ou inspeção manual.
- Deletar a fila principal também deleta sua DLQ.

### 5.4 Metadados Adicionados na DLQ

Quando uma mensagem é movida para a DLQ, os seguintes headers são adicionados
automaticamente:

| Header | Descrição |
|--------|-----------|
| `x-dlq-reason` | Motivo da movimentação (`max_retries_exceeded`, `nack_no_requeue`, `ack_timeout`) |
| `x-dlq-original-queue` | Nome da fila original |
| `x-dlq-moved-at` | Timestamp ISO 8601 da movimentação |
| `x-dlq-total-attempts` | Número total de tentativas de entrega |
| `x-dlq-last-nack-reason` | Motivo do último Nack (se aplicável) |

---

## 6. Goroutines de Background

O broker mantém goroutines de longa duração para tarefas periódicas:

| Goroutine | Escopo | Frequência | Descrição |
|-----------|--------|------------|-----------|
| TTL Reaper | 1 por fila (com TTL > 0) | A cada `ttl / 2` segundos (mín. 1s) | Varre mensagens pendentes e remove expiradas |
| Ack Timeout Monitor | 1 global | A cada 5 segundos | Varre o mapa `inFlight` de todas as filas e trata entregas cujo timeout expirou |
| Dispatcher | 1 por fila | Contínuo (bloqueado no channel) | Lê mensagens do channel e distribui para consumidores via round-robin |

Todas as goroutines de background são gerenciadas com `context.Context` para
shutdown gracioso. Quando o servidor recebe SIGINT/SIGTERM:

1. Fecha o listener HTTP e WebSocket (para de aceitar novas conexões)
2. Cancela o context raiz
3. Aguarda goroutines finalizarem (com timeout de 10s)
4. Fecha conexões WebSocket ativas com close frame 1001 (Going Away)

---

## 7. Limites e Defaults

| Parâmetro | Default | Mínimo | Máximo |
|-----------|---------|--------|--------|
| `ttl_seconds` | 0 (sem expiração) | 0 | 2.147.483.647 (~68 anos) |
| `max_size` | 0 (sem limite) | 0 | 0 (limitado pela memória) |
| `max_retries` | 3 | 1 | 100 |
| `ack_timeout_seconds` | 30 | 1 | 3600 |
| Tamanho máximo do body | 1 MB | — | 1 MB |
| Nome máximo da fila | 255 chars | 1 | 255 |
| Porta HTTP (management) | 8080 | — | — |
| Porta WebSocket (messaging) | 8081 | — | — |

---

## 8. Decisões Arquiteturais (ADRs)

### ADR-001: Channel como fila de mensagens

**Decisão:** Usar `chan *Message` buffered como estrutura de dados da fila.

**Contexto:** Precisamos de uma estrutura FIFO thread-safe para armazenar
mensagens pendentes.

**Alternativas consideradas:**
- Slice protegido por mutex: mais flexível mas mais complexo e propenso a bugs.
- Ring buffer custom: boa performance mas complexidade desnecessária para v1.

**Consequências:**
- ✅ Thread-safe nativamente sem locks adicionais
- ✅ Semântica de bloqueio built-in (dispatcher pode bloquear esperando mensagens)
- ✅ `len(ch)` retorna o número de mensagens pendentes (para métricas)
- ⚠️ Tamanho fixo (definido na criação). Filas sem `max_size` usam buffer grande (1.000.000)
- ⚠️ Não suporta remoção por posição (necessário para TTL — ver ADR-002)

### ADR-002: TTL via Reaper Goroutine

**Decisão:** Mensagens expiradas são removidas por uma goroutine de varredura
periódica, não no momento da leitura.

**Contexto:** Go channels não suportam remoção arbitrária por posição.

**Solução:** O TTL Reaper drena o channel para um buffer temporário, descarta
mensagens expiradas, e re-enfileira as válidas. Isso é feito com o mutex da
fila segurado para evitar entregas de mensagens expiradas durante a varredura.

**Consequências:**
- ✅ Simples de implementar
- ✅ Mensagens expiradas não são entregues a consumidores
- ⚠️ Há uma janela de tempo (entre varreduras) onde mensagens expiradas podem
  existir no channel. O dispatcher também verifica TTL antes de entregar.
- ⚠️ Custo O(n) por varredura — aceitável para filas de tamanho moderado

### ADR-003: Prioridade via Heap (Futuro)

**Decisão:** Na v1, o campo `priority` é aceito e armazenado mas **não afeta
a ordem de entrega** (FIFO puro). Suporte real a prioridade é planejado para v2
com a substituição do channel por um `container/heap`.

**Justificativa:** Manter a simplicidade da v1 usando channels. Prioridade
requer uma priority queue custom que é incompatível com `chan`.

---

## 9. Testes de Concorrência Planejados

Os testes a serem escritos na Fase 2 devem cobrir os seguintes cenários
e **todos devem passar com `go test -race`**:

| # | Cenário | Descrição |
|---|---------|-----------|
| T1 | Múltiplos produtores simultâneos | 100 goroutines publicando na mesma fila concorrentemente |
| T2 | Produtores + consumidores simultâneos | Publicação e consumo simultâneo na mesma fila |
| T3 | Subscribe/Unsubscribe durante delivery | Consumidores entrando e saindo durante entrega ativa |
| T4 | Ack/Nack concurrent | Múltiplos acks e nacks simultâneos para a mesma fila |
| T5 | Create/Delete durante publish | Criar e deletar filas enquanto publicações estão acontecendo |
| T6 | DLQ overflow | Mensagens sendo movidas para DLQ concorrentemente |
| T7 | TTL expiration during delivery | Mensagens expirando enquanto estão sendo processadas |
| T8 | Max retries exhaustion | Mensagem recebendo nack repetido até ir para DLQ |
| T9 | Graceful shutdown | Shutdown durante operações ativas — nenhuma goroutine leaka |