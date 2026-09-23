# rinotravel-api

API do RinoTravel, uma plataforma pessoal de gerenciamento de viagens. Backend 100% Go, com MongoDB Atlas.

## Status

Fases 1 a 10 concluídas no backend: fundação, autenticação, viagens com membros, roteiro, sync offline-first, lugares e restaurantes, voos e hotéis, gastos e orçamento, documentos (MongoDB GridFS) e adaptadores do Google.

| Fase | Escopo | Estado |
| --- | --- | --- |
| 1 | Esqueleto: config, servidor, health, logging, erros, conexão Mongo | pronta |
| 2 | User e autenticação | pronta |
| 3 | Trip e membros, permissões, base sync-ready | pronta |
| 4 | Roteiro (dias, itens, timeline unificada) | pronta |
| 5 | Sync: pull por cursor, push de mutations, idempotência, conflitos | pronta |
| 6 | Places e restaurantes | pronta |
| 7 | Voos e hotéis | pronta |
| 8 | Gastos e orçamento (substituiu deslocamentos, removidos para o app ficar como o Wanderlog) | pronta |
| 9 | Documentos, armazenados no próprio MongoDB (GridFS) | pronta |
| 10 | Google Places e Routes atrás de ports (opcional) | pronta |

O plano completo, com as decisões de cada fase, está em [docs/ROADMAP.md](docs/ROADMAP.md). O contrato HTTP está em [docs/openapi.yaml](docs/openapi.yaml).

## Arquitetura

Arquitetura hexagonal (ports and adapters), organizada por domínio e não por camada técnica.

```
HTTP handler → use case → domínio → interface (porta) → adapter (Mongo)
```

O domínio não conhece infraestrutura: não importa `net/http`, o driver do MongoDB nem nada de `platform`.

```
cmd/api/                  ponto de entrada: monta as dependências e sobe o servidor
internal/
  apperror/               tipo de erro da aplicação (kind + code + mensagem segura)
  server/                 rotas e composição dos middlewares
  kernel/                 value objects compartilhados: Date (civil), Timezone, Currency
  user/                   User, regras de cadastro, use case GetUser
    mongorepo/            adapter MongoDB
  trip/                   agregado Trip (com membros), política de permissões e use cases
    httpapi/              handlers HTTP
    mongorepo/            adapter MongoDB (transações, versão e seq)
    triptest/             repositório em memória para testes
  auth/                   Session, use cases Register, Login, Logout e Authenticate
    argon2id/             adapter do hash de senha
    httpapi/              handlers HTTP e Guard (autenticação por bearer)
    mongorepo/            adapter MongoDB das sessões
    authtest/             fakes em memória para testes
  platform/               infraestrutura compartilhada, sem regra de negócio
    config/               variáveis de ambiente → Config validada
    logging/              slog (JSON em staging/production, texto em development)
    requestid/            geração e propagação do request ID via context
    clock/                relógio UTC com precisão de milissegundo (a do Mongo)
    ids/                  UUIDv7
    httpx/                erros HTTP, decode de JSON, rate limit, middlewares
    mongodb/              conexão, transações e o contador de seq por viagem
```

Cada domínio segue o mesmo padrão: pacote raiz com entidades e use cases (as interfaces ficam ao lado de quem as consome), `httpapi/` para HTTP e `mongorepo/` para persistência.

Decisões que orientam as próximas fases:

- **Interfaces no consumidor.** Repositórios são declarados no pacote do use case que os usa, sem um pacote `ports/` separado.
- **`trip` engloba os membros.** `TripMember` faz parte do agregado Trip. Ficam embutidos no documento da Trip, o que dá atomicidade por documento sem transações.
- **Autorização na política do domínio.** Uma função pura decide o que cada papel pode fazer. Handlers só traduzem o resultado em HTTP.
- **Sessão opaca.** Login por email e senha (Argon2id). O token de sessão é aleatório e só o hash é guardado no banco, o que permite revogar sem biblioteca JWT. O cadastro exige um código de convite definido por variável de ambiente.

### Erros

Todo erro de API usa `application/problem+json` (RFC 9457) com `status`, `title`, `code` (estável, para o frontend), `detail`, `requestId` e, em validações, `errors[]` por campo.

| Status | Uso |
| --- | --- |
| 400 | requisição malformada (JSON inválido, ID mal formado) |
| 401 | sem autenticação ou credenciais inválidas |
| 403 | autenticado, sem permissão |
| 404 | recurso inexistente, ou que o usuário não pode saber que existe |
| 409 | conflito de estado (ex.: email já cadastrado) |
| 422 | dados bem formados que violam uma regra |
| 429 | limite de requisições excedido (com `Retry-After`) |
| 500 | erro interno, sempre com mensagem genérica (o detalhe vai só para o log) |

Use cases retornam `*apperror.Error`. Qualquer outro erro vira 500. O mapeamento fica em um único lugar, [internal/platform/httpx/response.go](internal/platform/httpx/response.go).

### Observabilidade

Cada requisição recebe um `X-Request-ID` (aceita o do cliente se for válido), devolvido na resposta e incluído automaticamente em todo log emitido com `slog.*Context`. O access log registra método, path (sem query string), rota, status e duração. Rastreamento de erros externo fica para depois, via um novo middleware.

## Autenticação

Login por email e senha. O cadastro exige o código de convite (`REGISTRATION_CODE`), que só quem administra a instância conhece.

| Método e rota | Auth | Descrição |
| --- | --- | --- |
| `POST /api/v1/auth/register` | código de convite | cria a conta e devolve a sessão (201) |
| `POST /api/v1/auth/login` | não | devolve uma nova sessão (200) |
| `POST /api/v1/auth/logout` | bearer | revoga a sessão atual (204) |
| `GET /api/v1/me` | bearer | usuário autenticado |

```sh
curl -X POST localhost:8080/api/v1/auth/register \
  -d '{"email":"ana@example.com","name":"Ana","password":"uma senha longa","registrationCode":"..."}'
# {"token":"rt_...","expiresAt":"...","user":{...}}

curl localhost:8080/api/v1/me -H "Authorization: Bearer rt_..."
```

Decisões de segurança:

- **Senha:** Argon2id (parâmetros mínimos da OWASP, gravados no próprio hash, o que permite evoluí-los), entre 10 e 128 caracteres.
- **Sessão:** token opaco de 256 bits. Só o SHA-256 dele é guardado, então um vazamento do banco não entrega tokens válidos. Vale 30 dias, com expiração automática (índice TTL), e pode ser revogada no logout.
- **Código de convite:** comparado em tempo constante e verificado antes de qualquer validação ou hash.
- **Login:** email desconhecido e senha errada devolvem a mesma resposta e gastam o mesmo tempo de hash, para não revelar quais emails existem.
- **Força bruta:** `register` e `login` compartilham um limite de 10 requisições por minuto por IP. Atrás de um proxy (Railway), defina `TRUST_PROXY=true` para usar o IP visto pelo proxy. Sem isso, todos os clientes cairiam no mesmo balde. O limite é em memória, por instância.
- **Respostas de token:** `Cache-Control: no-store`. Senhas, tokens e o código de convite nunca vão para os logs.

O cadastro devolve 409 quando o email já existe. Isso revela que o email está cadastrado, mas só a quem já possui o código de convite.

## Viagens e membros

Todas as rotas exigem `Authorization: Bearer`. Quem não é membro de uma viagem recebe **404**, idêntico ao de uma viagem inexistente, para não revelar que ela existe.

| Método e rota | Quem pode | Descrição |
| --- | --- | --- |
| `GET /api/v1/trips` | autenticado | viagens em que sou membro, com `myRole` |
| `POST /api/v1/trips` | autenticado | cria a viagem; quem cria vira OWNER. Aceita `id` opcional (UUID gerado no cliente) |
| `GET /api/v1/trips/{id}` | qualquer membro | detalhes |
| `PATCH /api/v1/trips/{id}` | OWNER, ADMIN | atualização parcial; exige `baseVersion` |
| `DELETE /api/v1/trips/{id}` | OWNER | soft delete (204) |
| `GET /api/v1/trips/{id}/members` | qualquer membro | membros, OWNER primeiro |
| `POST /api/v1/trips/{id}/members` | OWNER, ADMIN | adiciona por `email` e `role` |
| `PATCH /api/v1/trips/{id}/members/{userId}` | OWNER, ADMIN | muda o papel |
| `DELETE /api/v1/trips/{id}/members/{userId}` | OWNER, ADMIN, ou o próprio membro | remove ou sai da viagem |
| `POST /api/v1/trips/{id}/transfer-ownership` | OWNER | passa a posse para outro membro |

### Permissões

| Ação | OWNER | ADMIN | MEMBER | VIEWER |
| --- | :-: | :-: | :-: | :-: |
| Ler viagem e conteúdo | sim | sim | sim | sim |
| Escrever conteúdo | sim | sim | sim | não |
| Editar dados da viagem | sim | sim | não | não |
| Adicionar, remover ou alterar MEMBER/VIEWER | sim | sim | não | não |
| Promover a ADMIN, rebaixar ou remover ADMIN | sim | não | não | não |
| Transferir a posse e deletar a viagem | sim | não | não | não |
| Sair da viagem | não (transfere antes) | sim | sim | sim |

A regra vive em um único lugar, a função `Can(role, action)` em [internal/trip/policy.go](internal/trip/policy.go). Os handlers nunca decidem permissão. Invariantes do agregado: existe sempre exatamente um OWNER, e a posse só muda por `transfer-ownership` (o dono anterior vira ADMIN).

### Concorrência

Toda viagem tem uma `version`. O `PATCH` exige o `baseVersion` que o cliente viu: se outra pessoa alterou antes, a resposta é `409 version_conflict` e nada é sobrescrito. Mudanças de membros também incrementam a versão. No Mongo a escrita é condicional (`version` esperada) e roda em transação.

### Base para sincronização

Cada escrita recebe um número de sequência (`seq`) por viagem, alocado dentro da mesma transação que grava o dado. Como todas as escritas de uma viagem passam pelo mesmo contador, elas se serializam e a ordem de commit é a ordem de `seq`, o que permite ao cliente usar `seq` como cursor sem perder mudanças. O protocolo completo está em [docs/ROADMAP.md](docs/ROADMAP.md).

## Roteiro, reservas e demais domínios

Cada domínio segue o mesmo formato (entidades e use cases no pacote raiz, `httpapi/` e `mongorepo/`) e é montado em `internal/app`.

| Domínio | Rotas principais |
| --- | --- |
| Roteiro | `itinerary-days`, `itinerary-items`, `itinerary-items/from-place`, `GET itinerary` (timeline unificada) e `maps/day` (trechos, tempos e traçado do dia; só com chave do Google) |
| Lugares | `places`, `restaurants`, `GET /places/search` e `maps/location` (mapa de um local em PNG; só com chave do Google) |
| Gastos | `expenses` (gastos planejados e pagos, ligados a lugares) e `budget-limits` (orçamento da viagem e por categoria) |
| Reservas | `flights`, `hotels` (duração do voo e fusos derivados no servidor), `tickets` (ingressos: onde e quando são usados, com o arquivo apontando para um documento da viagem) |
| Documentos | `documents`, `documents/{id}/complete`, `documents/{id}/download` |
| Sync | `GET` e `POST /trips/{id}/sync` |

Todas as rotas de conteúdo ficam sob `/api/v1/trips/{tripId}/...`, exigem membership (404 para quem não é membro) e seguem a matriz de papéis. Os horários trafegam como `{dateTime, timezone}` (hora local e fuso IANA); o instante absoluto é sempre derivado, nunca gravado. Valores em dinheiro usam a menor unidade da moeda (centavos, ienes inteiros).

### Sync offline-first

`GET /trips/{id}/sync?cursor=` devolve as mudanças da viagem em ordem, inclusive exclusões, com um cursor opaco. `POST` aplica mutations feitas offline, uma por vez, pelos mesmos use cases da API REST, e responde um resultado por mutation (`applied`, `duplicate`, `conflict` ou `rejected`). Reenviar o mesmo `mutationId` é seguro. Conflitos são detectados por versão (`baseVersion`) e nunca sobrescrevem em silêncio. O protocolo completo está na seção 5 do [ROADMAP](docs/ROADMAP.md).

### Documentos e armazenamento

Os arquivos ficam no **MongoDB (GridFS)**, sem nenhum serviço externo de armazenamento. O upload tem três passos: `POST documents` registra o documento como `PENDING` e devolve um link assinado; o cliente envia os bytes com `PUT` nesse link; `POST documents/{id}/complete` confere tamanho e SHA-256 e passa o documento para `READY`. O download devolve um link assinado de curta duração. Os links são HMAC-SHA256 com expiração, operação, tamanho e checksum embutidos, e a autorização é o próprio token. Limites: 25 MB por arquivo e PDF, JPEG, PNG, WebP ou HEIC.

### Google (opcional)

Sem `GOOGLE_MAPS_API_KEY` a API sobe normalmente e as rotas do Google simplesmente não existem; o cadastro manual continua funcionando. Com a chave, ative no Google Cloud a **Places API (New)**, a **Routes API** e a **Maps Static API**, crie uma chave de servidor e restrinja-a a essas três APIs. A Routes API calcula os trechos do mapa do dia (`maps/day`) e a Maps Static API desenha as imagens (10.000 chamadas grátis por mês); tudo é montado na hora e entregue direto ao cliente, sem guardar nada, porque os termos do Google não permitem armazenar o conteúdo deles. A chave nunca vai para o frontend.

**Teto de gasto.** O Google não tem um modo "nunca me cobre": os orçamentos só enviam alertas. Por isso o backend conta as chamadas no MongoDB (coleção `provider_usage`, um documento por API e mês) e, ao atingir `GOOGLE_MONTHLY_LIMIT`, passa a recusar novas chamadas com `503 provider_quota_exhausted`, sem contatar o Google. O padrão de 4000 fica abaixo da menor franquia gratuita (5.000 chamadas), deixando margem para a diferença entre o mês do Google e o mês UTC usado na contagem. A chamada é contada antes de ser enviada e nunca devolvida, porque uma requisição que falha depois de sair ainda pode ser cobrada. Se o contador estiver indisponível, a chamada é recusada em vez de liberada. Um aviso vai para o log ao chegar em 80% do limite e a cada recusa. O contador é atômico: várias instâncias da API, ou requisições simultâneas, nunca passam do limite.

## Stack

- Go 1.26+ (`net/http` com `ServeMux`, `log/slog`)
- MongoDB Atlas (dados e arquivos, via GridFS) e o [driver oficial v2](https://pkg.go.dev/go.mongodb.org/mongo-driver/v2)
- golangci-lint 2.x

## Setup

Pré-requisitos: Go 1.26+, golangci-lint e, para o banco local, Docker.

```sh
cp .env.example .env
make db-up
```

`make db-up` sobe um MongoDB local via Docker Compose. Para usar o Atlas, aponte `MONGODB_URI` para o cluster no `.env`.

O MongoDB precisa ser um **replica set** (o Atlas sempre é), porque as escritas usam transações. O Compose já configura um replica set de um nó. Um `mongod` standalone é recusado na inicialização com uma mensagem explicando o motivo.

Os arquivos `.env` são carregados pelo shell: mantenha as aspas em valores com `&`, como a URI de exemplo.

## Variáveis de ambiente

| Variável | Obrigatória | Padrão | Descrição |
| --- | --- | --- | --- |
| `APP_ENV` | sim | | `development`, `staging` ou `production` |
| `MONGODB_URI` | sim | | string de conexão do MongoDB |
| `MONGODB_DATABASE` | sim | | nome do banco (use um por ambiente) |
| `REGISTRATION_CODE` | sim | | código de convite do cadastro (mínimo de 12 caracteres) |
| `PORT` | não | `8080` | porta HTTP |
| `LOG_LEVEL` | não | `info` | `debug`, `info`, `warn` ou `error` |
| `CORS_ALLOWED_ORIGINS` | não | vazio | origens permitidas, separadas por vírgula |
| `AUTH_RATE_LIMIT` | não | `10` | requisições por minuto, por IP, em `register` e `login` |
| `TRUST_PROXY` | não | `false` | `true` quando há um proxy reverso confiável na frente (usa o último `X-Forwarded-For`) |
| `STORAGE_SIGNING_SECRET` | não | vazio | segredo (mínimo de 32 caracteres) que assina os links de documentos. Vazio desliga os documentos |
| `API_PUBLIC_URL` | com o segredo acima | | URL pela qual os clientes alcançam esta API; entra nos links assinados |
| `GOOGLE_MAPS_API_KEY` | não | vazio | chave de servidor (Places API New e Routes API). Vazio desliga a busca de lugares e o cálculo de rotas |
| `GOOGLE_MONTHLY_LIMIT` | não | `4000` | teto de chamadas ao Google por mês, para cada API (busca e rotas). `0` desliga o teto |

A aplicação não sobe se alguma variável obrigatória estiver ausente ou inválida, e lista todos os problemas de uma vez. O `.env` está no `.gitignore`: nunca commite credenciais. Em staging e production, configure as variáveis no provedor de deploy.

## Rodar localmente

```sh
make run
curl http://localhost:8080/api/v1/health
# {"status":"ok"}
```

## Testes

```sh
make test               # unitários, com -race
make test-integration   # repositórios Mongo; exigem `make db-up` e o .env
```

Testes de integração usam a build tag `integration`, criam um banco descartável por teste e o removem ao final. Nunca os aponte para o Atlas de production.

## Lint e formatação

```sh
make lint
make fmt
make check    # gofmt, vet, lint, testes e build
```

## Build

```sh
make build                         # bin/api
docker build -t rinotravel-api .   # imagem distroless, roda como não-root
```

## Deploy (Railway)

O backend roda como container (o `Dockerfile` já produz uma imagem distroless). No Railway:

1. Crie o serviço a partir deste repositório; o Railway detecta o `Dockerfile`.
2. Configure as variáveis: `APP_ENV=production`, `MONGODB_URI` (Atlas), `MONGODB_DATABASE`, `REGISTRATION_CODE`, `TRUST_PROXY=true`, `CORS_ALLOWED_ORIGINS=https://<seu-app>.vercel.app`, `STORAGE_SIGNING_SECRET`, `API_PUBLIC_URL=https://<seu-servico>.up.railway.app` e, se quiser, `GOOGLE_MAPS_API_KEY`. O Railway define `PORT` sozinho.
3. Use `/api/v1/health` como healthcheck.
4. No Atlas, libere o acesso de rede do Railway e mantenha o cluster como replica set (o Atlas já é).

Os índices (TTL, únicos e parciais) são criados na inicialização.
