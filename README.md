# WhatsApp Support Core

MVP demonstrativo de atendimento em Go, PostgreSQL e Docker Compose. A integração real com WhatsApp não está implementada. O provider de IA pode ser local ou OpenAI; o provider local é o padrão.

## Escopo e segurança

Este projeto é apenas local/demonstrativo: não possui autenticação, não implementa WhatsApp real e não é uma solução de produção. Não o exponha publicamente nem use dados reais de clientes. O Compose publica as portas da API e do PostgreSQL no host.

Não faça commit de `.env` ou de outros arquivos de ambiente. `.gitignore` ignora `.env` e `.env.*`, com exceção de `.env.example`; este exemplo contém apenas placeholders fictícios. `.dockerignore` exclui esses arquivos do contexto de build. Não passe secrets como arquivos ou argumentos de build Docker.

## Requisitos

- Docker com Docker Compose
- Go 1.27 ou superior para executar os testes no host

## Configuração

Copie `.env.example` para `.env` para a configuração local do Compose. Por padrão, `AI_PROVIDER=local` e nenhuma credencial é necessária. O Compose lê `.env` automaticamente.

Para selecionar OpenAI, configure no `.env`:

```env
AI_PROVIDER=openai
OPENAI_API_KEY=sua-chave-real-local
OPENAI_MODEL=nome-de-um-modelo-disponivel
```

`OPENAI_API_KEY` e `OPENAI_MODEL` são necessárias somente com `AI_PROVIDER=openai`. Use um modelo disponível para sua conta. Nunca coloque a chave no código, Git ou imagem Docker. Para voltar ao modo local, defina `AI_PROVIDER=local`. Os testes não chamam a OpenAI.

Para executar o binário Go diretamente no host, configure também `DATABASE_URL` e, opcionalmente, `PORT` no ambiente do processo. O binário não carrega `.env` por conta própria.

## Execução

Na raiz do projeto:

```sh
docker compose up --build
```

O Compose inicia PostgreSQL e API. A API aplica migrations pendentes ao iniciar e escuta em `http://localhost:8080`. Verifique a API com `GET http://localhost:8080/healthz`.

## Testes

Testes unitários e HTTP, sem PostgreSQL ou serviços externos:

```sh
go test ./...
```

Os testes PostgreSQL são executados quando `TEST_DATABASE_URL` está configurada; sem ela, os testes de integração são ignorados. Para executá-los no PowerShell, use um banco de teste dedicado que já tenha recebido as migrations 0001–0004:

```powershell
$env:TEST_DATABASE_URL = 'postgres://app:app@localhost:5432/concurrency_test?sslmode=disable'
go test ./...
Remove-Item Env:TEST_DATABASE_URL
```

Para preparar um banco de teste no PostgreSQL do Compose, inicie `docker compose up -d postgres`, crie o banco com `docker compose exec postgres createdb -U app concurrency_test` e inicie a API temporariamente apontando para ele:

```powershell
docker compose run --rm --no-deps -e DATABASE_URL='postgres://app:app@postgres:5432/concurrency_test?sslmode=disable' -e PORT=8081 -e AI_PROVIDER=local -e OPENAI_API_KEY= -e OPENAI_MODEL= api
```

Aguarde a mensagem de migrations aplicadas e interrompa o processo com `Ctrl+C`; então execute os comandos de teste acima. Use um banco descartável, pois os testes criam e removem registros de validação.

## Migrations

- `0001`: cria contatos, conversas, mensagens, estados permitidos, chaves estrangeiras e índices básicos.
- `0002`: adiciona `external_id` único para inbound, referência da resposta à mensagem original e unicidade de resposta por inbound.
- `0003`: adiciona a claim de processamento `processing_started_at`, restrita a mensagens inbound.
- `0004`: cria índice único parcial para permitir no máximo uma conversa não resolvida por contato. **Pressupõe que não existam conversas ativas duplicadas antes da aplicação**; se houver, a migration falha e os dados precisam ser reconciliados antes. Ela não remove nem combina conversas.

As migrations pendentes são aplicadas pela API ao iniciar.

## Provider de IA

O provider local retorna uma resposta fixa. O provider OpenAI usa a Responses API com saída JSON Schema estrita para `content` e `requires_human`; o cliente HTTP tem timeout de 30 segundos. Erros de quota retornam `503 Service Unavailable`; outras falhas do provider retornam `502 Bad Gateway`. As respostas HTTP de erro são genéricas e os logs mantêm diagnóstico limitado, sem corpo completo do provider.

## Demo

Use o provider local nesta demonstração. Ele retorna sempre a mesma resposta e não exige nem chama a OpenAI. Em um primeiro terminal, na raiz do projeto, force o modo local e suba a API e o PostgreSQL:

```powershell
$env:AI_PROVIDER = 'local'
docker compose up --build
```

A API aplica as migrations pendentes ao iniciar. Mantenha esse terminal aberto. Em um segundo PowerShell, execute os passos abaixo.

### 1. Enviar a primeira mensagem

```powershell
$base = 'http://localhost:8080'
$phone = '5511990000001'
$firstBody = @{ phone = $phone; content = 'Olá, gostaria de informações'; external_id = 'demo-001' } | ConvertTo-Json -Compress
$firstResponse = Invoke-WebRequest -Method Post -Uri "$base/api/simulated/whatsapp/messages" -ContentType 'application/json; charset=utf-8' -Body $firstBody
$firstResponse.StatusCode # esperado: 201
$first = $firstResponse.Content | ConvertFrom-Json
$conversationId = $first.conversation.id
$first
```

O resultado contém uma conversa `bot`, a inbound `demo-001` e a resposta fixa: `Olá! Recebi sua mensagem. Como posso ajudar?`.

### 2. Consultar a conversa

```powershell
$conversation = Invoke-RestMethod -Method Get -Uri "$base/api/conversations/$conversationId"
$conversation.messages | Select-Object direction, sender, content, external_id
```

Guarde o `conversationId` retornado pela primeira chamada; a consulta mostra a inbound e o outbound persistidos.

### 3. Demonstrar idempotência

Reenvie exatamente o mesmo corpo, incluindo `external_id` `demo-001`:

```powershell
$duplicateResponse = Invoke-WebRequest -Method Post -Uri "$base/api/simulated/whatsapp/messages" -ContentType 'application/json; charset=utf-8' -Body $firstBody
$duplicateResponse.StatusCode # esperado: 200
$duplicate = $duplicateResponse.Content | ConvertFrom-Json
$duplicate.duplicate # esperado: True
$afterDuplicate = Invoke-RestMethod -Method Get -Uri "$base/api/conversations/$conversationId"
$afterDuplicate.messages.Count # continua 2; não cria nova mensagem nem chama o provider
```

### 4. Enviar uma segunda mensagem e consultar o histórico

```powershell
$secondBody = @{ phone = $phone; content = 'Também quero saber os horários'; external_id = 'demo-002' } | ConvertTo-Json -Compress
$secondResponse = Invoke-WebRequest -Method Post -Uri "$base/api/simulated/whatsapp/messages" -ContentType 'application/json; charset=utf-8' -Body $secondBody
$secondResponse.StatusCode # esperado: 201
$second = $secondResponse.Content | ConvertFrom-Json
$second.conversation.id # igual a $conversationId
$conversation = Invoke-RestMethod -Method Get -Uri "$base/api/conversations/$conversationId"
$conversation.messages | Select-Object direction, sender, content, external_id
```

A consulta mostra as duas inbounds e suas respostas na mesma conversa. O serviço envia mensagens anteriores como histórico ao provider; o `LocalProvider` mantém a resposta fixa, portanto a resposta textual não muda com o histórico.

### 5. Assumir o atendimento

```powershell
$takeover = Invoke-RestMethod -Method Post -Uri "$base/api/conversations/$conversationId/takeover"
$takeover.status # esperado: human
```

### 6. Enviar uma mensagem humana

```powershell
$humanBody = @{ content = 'Olá, vou continuar seu atendimento.' } | ConvertTo-Json -Compress
$humanMessage = Invoke-RestMethod -Method Post -Uri "$base/api/conversations/$conversationId/messages" -ContentType 'application/json; charset=utf-8' -Body $humanBody
$humanMessage.sender # esperado: human
```

### 7. Resolver e consultar o estado final

```powershell
$resolved = Invoke-RestMethod -Method Post -Uri "$base/api/conversations/$conversationId/resolve"
$resolved.status # esperado: resolved
$final = Invoke-RestMethod -Method Get -Uri "$base/api/conversations/$conversationId"
$final.conversation.status # esperado: resolved
$final.messages | Select-Object direction, sender, content, external_id
```

Ao terminar, pressione `Ctrl+C` no primeiro terminal para parar os containers. Isso mantém o volume PostgreSQL; a próxima execução continuará com os dados da demonstração. O fluxo usa somente dados fictícios e o provider local.

## Frontend de demonstração

O frontend requer Node.js e pnpm. O frontend React/Vite consome a API atual e usa o proxy do servidor de desenvolvimento para encaminhar `/api` e `/healthz` ao backend. Assim, o navegador acessa a mesma origem e não exige configuração de CORS no backend.

1. Em um terminal, na raiz, suba o backend com provider local:

   ```powershell
   $env:AI_PROVIDER = 'local'
   docker compose up --build
   ```

2. Em outro terminal, configure e inicie o frontend:

   ```powershell
   cd web
   Copy-Item .env.example .env
   pnpm install
   pnpm dev
   ```

3. Abra `http://localhost:5173`. `VITE_API_URL` em `web/.env` define o destino do proxy; o exemplo aponta para `http://localhost:8080`. O `.env` local não deve ser commitado.

A interface mostra conversas existentes da API e permite simular uma entrada, visualizar o histórico, assumir o atendimento, enviar mensagem humana e resolver a conversa. Ela utiliza `/healthz`, `GET /api/conversations`, `GET /api/conversations/{id}`, `POST /api/simulated/whatsapp/messages`, `POST /api/conversations/{id}/takeover`, `POST /api/conversations/{id}/messages` e `POST /api/conversations/{id}/resolve`. Os contadores vêm da lista retornada pela API; nenhuma métrica é simulada.

Para validar a compilação do frontend, em `web/`, execute `pnpm build`.

## Estrutura

- `cmd/api`: configuração, conexão e inicialização.
- `internal/service`: modelos e regras de aplicação.
- `internal/ai`: contrato de provider e resposta local.
- `internal/postgres`: repositories PostgreSQL.
- `internal/httpapi`: endpoints REST e conversão HTTP/JSON.
- `migrations`: migrations SQL aplicadas automaticamente na inicialização.

## Escopo futuro

Uma interface de atendente e a integração com a WhatsApp Business Platform não fazem parte deste MVP. Mantenha o projeto em ambiente local e use somente dados fictícios.
