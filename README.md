# rinotravel-api

API do RinoTravel, uma plataforma pessoal de gerenciamento de viagens. Backend 100% Go, com MongoDB Atlas.

## Status

Fases 1 a 3 concluídas: fundação (config, servidor, logging, erros, CORS, Mongo, health), autenticação (cadastro com código de convite, login, sessão, `/me`) e viagens com membros e permissões.

| Fase | Escopo | Estado |
| --- | --- | --- |
| 1 | Esqueleto: config, servidor, health, logging, erros, conexão Mongo | pronta |
| 2 | User e autenticação | pronta |
| 3 | Trip e membros, permissões, base sync-ready | pronta |
| 4 | Roteiro (dias, itens, timeline) | próxima |
| 5 a 10 | Sync, places, voos e hotéis, transfers, documentos, Google | planejadas |

O plano completo, com as decisões de cada fase, está em [docs/ROADMAP.md](docs/ROADMAP.md).

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

## Stack

- Go 1.26+ (`net/http` com `ServeMux`, `log/slog`)
- MongoDB Atlas e o [driver oficial v2](https://pkg.go.dev/go.mongodb.org/mongo-driver/v2)
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
| `TRUST_PROXY` | não | `false` | `true` quando há um proxy reverso confiável na frente (usa o último `X-Forwarded-For`) |

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
