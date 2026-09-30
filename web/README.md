# Protótipo web — Atende

Interface local de demonstração para o MVP de atendimento. O frontend consome somente os endpoints REST existentes; não implementa WhatsApp real e usa o provider local do backend.

## Requisitos

- Docker Compose para API e PostgreSQL
- Node.js
- pnpm

## Executar

Abra dois terminais na raiz do repositório.

### 1. Backend

No primeiro terminal, inicie API e PostgreSQL com o provider local:

```powershell
$env:AI_PROVIDER = 'local'
docker compose up --build
```

A API aplica migrations pendentes e fica em `http://localhost:8080`. Mantenha o terminal aberto.

### 2. Frontend

No segundo terminal:

```powershell
cd web
Copy-Item .env.example .env
pnpm install
pnpm dev
```

Abra `http://localhost:5173`. `VITE_API_URL` em `web/.env` define o destino do proxy Vite; o exemplo aponta para `http://localhost:8080`. As chamadas passam pelo proxy local para evitar CORS, sem mudanças no backend.

Para compilar a versão de produção:

```powershell
pnpm build
```

## Usar a demonstração

- **Visão geral:** contadores calculados a partir das conversas retornadas pela API.
- **Simular mensagem:** informa telefone e texto fictícios; o frontend gera um `external_id` e chama a rota de simulação.
- **Conversas:** filtre por automático, humano ou resolvido e abra uma conversa para ver as mensagens cronológicas.
- **Atendimento:** assuma a conversa, envie uma mensagem humana e resolva-a. Conversas resolvidas ficam somente para leitura.
- **Configurações:** veja o estado da API consultado em `/healthz` e os componentes do ambiente local.

Use apenas dados fictícios. Este protótipo não tem autenticação e deve permanecer em ambiente local.
