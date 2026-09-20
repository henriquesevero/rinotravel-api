# RinoTravel API: roadmap consolidado

Documento único que resume as decisões e as fases planejadas. Consolida os três prompts de escopo e a análise técnica de cada um.

## 1. Princípios

- Go idiomático, `net/http`, `log/slog`. 100% Go, MongoDB Atlas, driver oficial.
- Hexagonal organizada **por domínio**. O domínio não importa `net/http`, driver do Mongo, SDK da AWS nem `platform`.
- Interfaces declaradas no consumidor (pacote do use case), sem pacote `ports/` separado.
- Use cases representam ações. Handlers só fazem HTTP, parsing, contexto do ator e resposta.
- Sem código morto: nada é criado antes de haver um use case que o consuma.
- Sem comentários óbvios. Comentário só para decisão não óbvia, restrição externa ou arquitetural.
- Fatias verticais: cada fase entrega domínio, use cases, Mongo, HTTP e testes, com endpoints funcionando.
- Todo prompt de fase começa com: "analise a implementação atual e respeite a arquitetura existente".
- Gate de toda fase: `make check` (gofmt, vet, golangci-lint, `go test -race`, build).

## 2. Decisões fechadas

| Tema | Decisão |
| --- | --- |
| Autenticação | email e senha (Argon2id); cadastro exige código de convite (env var, comparação em tempo constante); token de sessão opaco, só o hash SHA-256 no banco; `Authorization: Bearer` |
| Membros | embutidos no documento da Trip; invariante de exatamente um OWNER; `OwnerID` derivado do membro OWNER |
| IDs | tipo próprio no domínio (UUIDv7 em string), sem `bson.ObjectID`; o cliente pode gerar o ID (offline) |
| Erros | `apperror` + RFC 9457; 400 malformado, 422 regra violada, 404 para não-membro |
| Concorrência | `Version` em toda entidade mutável; escrita condicional; conflito devolve 409 |
| Local dev | Docker Compose com Mongo. **Replica set de um nó** desde a fase 3 (transações, ver seção 5); um Mongo standalone é recusado no boot. Sem MinIO nem AWS: os arquivos ficam no próprio Mongo (GridFS) |

### Matriz de permissões

| Ação | OWNER | ADMIN | MEMBER | VIEWER |
| --- | :-: | :-: | :-: | :-: |
| Ler viagem e conteúdo | sim | sim | sim | sim |
| Escrever conteúdo (itens, places, voos, hotéis, transfers, documentos) | sim | sim | sim | não |
| Editar dados da viagem | sim | sim | não | não |
| Adicionar, remover ou alterar MEMBER/VIEWER | sim | sim | não | não |
| Promover a ADMIN, rebaixar ou remover ADMIN | sim | não | não | não |
| Transferir a posse (`TransferOwnership`) | sim | não | não | não |
| Deletar a viagem | sim | não | não | não |
| Sair da viagem | não (transfere antes) | sim | sim | sim |

Não-membro recebe 404. A regra vive numa política pura do domínio (`Can(role, action)`), chamada por um único `Authorizer`. Nenhum handler decide permissão.

## 3. Fases

| Fase | Escopo |
| --- | --- |
| 2 | **Pronta.** User e autenticação: cadastro com convite, login, sessão, `Guard`, `GET /api/v1/me`, rate limit em register e login |
| 3 | **Pronta.** Trip e membros: domínio, matriz acima, `TransferOwnership`, `GET /trips`, endpoints de membros. Base sync-ready: `version`, soft delete e `seq` por viagem em transação (seção 5.1). O `Authorizer` reutilizável ficou para a fase 4, quando surge o primeiro consumidor |
| 4 | **Pronta.** Itinerary: `Authorizer`, `ItineraryDay`, `ItineraryItem`, timeline calculada e unificada entre fontes, Place→Item |
| 5 | **Pronta.** Sync: pull incremental por cursor, push de mutations, idempotência, conflitos, com fontes pluggáveis por entidade |
| 6 | **Pronta.** Place e Restaurant (CRUD completo, registrados no sync) |
| 7 | **Pronta.** Flight e Hotel, com duração e fusos derivados |
| 8 | **Pronta.** Transfer e etapas, com planejamento de rota |
| 9 | **Pronta.** Documents no MongoDB GridFS (upload e download por link assinado, registrados no sync) |
| 10 | **Pronta.** Adapters do Google (`PlaceProvider`, `RouteProvider`), com os ports nascendo aqui; opcionais, ligados pela chave, com teto mensal de chamadas contado no MongoDB (`GOOGLE_MONTHLY_LIMIT`) |

Toda entidade nova a partir da fase 4 nasce sync-ready, e "registrar no sync" é critério de aceite da fase dela. Expense, Shopping e Checklist ficam fora até existirem; o sync é pluggable para recebê-los.

Pacotes: `trip`, `user`, `itinerary`, `place` (Place e Restaurant), `booking` (Flight e Hotel), `transfer`, `document`, `sync`. Cada um segue o mesmo padrão: pacote raiz (entidades e use cases), `httpapi/` e `mongorepo/`, nomes que não colidem com `net/http` nem com o driver `mongo`. Os pacotes se falam pelo `Authorizer` (interface no consumidor), sem ciclos.

## 4. Regras transversais de domínio

**Tempo**
- `Trip.StartDate`, `EndDate`, `ItineraryDay.Date` e datas de hotel são **datas civis**, não instantes.
- Todo evento com horário guarda instante em UTC **mais o fuso IANA do local**. Voos têm fuso de partida e de chegada. O dia de um item é o dia local no fuso do evento.
- `time/tzdata` embutido no binário (imagem mínima).
- Regras: `End >= Start`, dias dentro do intervalo da Trip, `(TripID, Date)` único.

**Dinheiro:** inteiro em menor unidade mais moeda ISO 4217. Nunca `float`.

**Localização:** value object `Location` (nome, endereço, coordenadas opcionais). Latitude entre -90 e 90, longitude entre -180 e 180, sempre as duas ou nenhuma.

**Valores derivados, não armazenados:** duração de item com início e fim; total e duração do Transfer (soma dos legs). Os legs ficam embutidos no Transfer, e a ordem é a posição no slice.

**Timeline:** `GetItinerary` monta uma lista ordenada por dia com itens, voos, hotéis, transfers e reservas de restaurante, sem duplicar dados. Item manual só tem categorias RESTAURANT, ATTRACTION, SHOPPING, FREE_TIME e OTHER. FLIGHT, HOTEL e TRANSPORTATION saem, porque são entidades próprias. Sobreposição é permitida.

**Segurança de dados**
- Todo use case confirma que os filhos citados pertencem à `TripID` da rota (evita IDOR).
- Códigos de reserva (`BookingCode`, `ConfirmationCode`, `ReservationCode`) são ocultados para VIEWER, inclusive no sync.
- `BookingURL` só aceita `http(s)`. Aeroporto é IATA de 3 letras. Textos têm limite de tamanho.

**Modelagem:** toda entidade tem `Version`, `CreatedAt`, `UpdatedAt`, `DeletedAt` opcional e autor. Enums de Place e Transfer precisam ser definidos na fase 6 e 8.

## 5. Protocolo de sincronização (fase 5)

### 5.1 Base sync-ready (a partir da fase 3)

- `seq`: contador monotônico **por viagem**, atribuído pelo servidor em toda escrita e indexado como `(tripId, seq)`. Fica no adapter Mongo; o domínio só conhece `Version`.
- **Transações Mongo** em cada escrita: incrementar o contador da viagem e gravar a entidade (e, no push, o registro da mutation) no mesmo passo. Como todas as escritas de uma viagem tocam o mesmo documento contador, elas se serializam, então a ordem de commit é a ordem de `seq`. Isso evita perder mudanças por commits fora de ordem, problema clássico de cursor por `updatedAt`.
- **Soft delete:** delete vira tombstone (`deletedAt`, novo `seq`). Leituras normais filtram `deletedAt`, centralizado no repositório. Tombstones expiram por TTL após 90 dias.
- Relógio do cliente nunca é autoridade. `updatedAt` e `seq` são do servidor.

### 5.2 Pull: `GET /api/v1/trips/{id}/sync?cursor=&limit=`

Resposta:

```json
{
  "changes": [
    {"entity": "itinerary_item", "id": "...", "op": "upsert", "version": 4, "record": {}},
    {"entity": "itinerary_item", "id": "...", "op": "delete", "version": 5}
  ],
  "cursor": "opaco",
  "hasMore": false,
  "resetRequired": false,
  "serverTime": "2026-09-19T20:00:00Z"
}
```

- Sem `cursor`: sincronização inicial completa, sem tombstones, paginada.
- `changes` ordenado por `seq`. "Criado" e "atualizado" viram `upsert`, porque o cliente decide pelo estado local.
- Paginação com merge entre coleções: busca-se as N primeiras de cada coleção, ordena-se e corta-se em N. O cursor é o `seq` do último item devolvido.
- O cursor é opaco (contém `seq` e o instante de emissão). Se for mais antigo que a retenção de tombstones, a resposta traz `resetRequired: true` e o cliente descarta o local e refaz a carga inicial.
- Toda query filtra por `tripId`. Nunca retorna dados de outra viagem. O mesmo redator por papel da API normal é aplicado.
- Não-membro recebe 404, e o cliente apaga a cópia local dessa viagem (remoção do membro).
- A lista de viagens do usuário (para descobrir viagens novas, removidas ou apagadas) vem de `GET /api/v1/trips`, uma lista pequena sem cursor.

### 5.3 Push: `POST /api/v1/trips/{id}/sync`

Mutation:

```json
{"mutationId": "uuid", "entity": "itinerary_item", "entityId": "uuid",
 "operation": "UPDATE", "baseVersion": 3, "clientTimestamp": "...", "payload": {}}
```

- O push é só **transporte**: cada mutation é despachada para o **mesmo use case** da API REST, com o mesmo ator, validação e autorização. Nunca escreve direto no repositório.
- Processadas em ordem, no máximo 100 por requisição. A resposta traz um resultado por mutation: `applied`, `duplicate`, `conflict`, `rejected` (com o código do erro) e o registro atual quando aplicável.
- **Idempotência:** coleção `sync_mutations` com índice único `(userId, mutationId)` e TTL. Guarda o hash do payload e o resultado. Repetição devolve o resultado guardado sem reaplicar. Mesmo `mutationId` com payload diferente é erro 422.
- CREATE usa o ID gerado pelo cliente. Se o ID já existir em outra viagem, a resposta é um conflito genérico, sem revelar nada.
- `payload` de UPDATE é parcial (só os campos alterados), decodificado estritamente por entidade.
- Papel insuficiente ou revogado durante o período offline gera `rejected: forbidden` por mutation. O cliente precisa tratar mutations rejeitadas.

### 5.4 Conflitos

Estratégia inicial, explícita: **checagem otimista por versão**.

- UPDATE e DELETE enviam `baseVersion`. Se for igual à versão atual, aplica e incrementa. Se não, retorna `conflict` com o registro atual do servidor. Nada é sobrescrito em silêncio.
- **Last Write Wins por `clientTimestamp` não é usado**, porque relógio de dispositivo offline não é confiável. O timestamp é só informativo.
- Casos: DELETE de algo já apagado é sucesso idempotente. UPDATE de algo apagado retorna `conflict: entity_deleted`. Aplicar dados idênticos ao estado atual é no-op.
- Como o UPDATE é parcial, o merge por campo pode ser adicionado depois sem mudar o protocolo.

## 6. Documentos (fase 9)

**Decisão: os arquivos ficam no MongoDB (GridFS).** O projeto não usa AWS. Backend no Railway, frontend na Vercel e todo o armazenamento no Atlas: um único lugar para backup, acesso e custo. O limite de 25 MB por arquivo cabe folgado no GridFS. Se um dia o volume crescer, o port `Storage` permite trocar por um object storage sem tocar no domínio.

**Modelo.** `Document` com `Version`, `Checksum` (SHA-256 hex), `Size`, `MimeType`, `FileName`, `Type`, `Status` (`PENDING`, `READY`), `OwnerID`, `Visibility` (`TRIP` ou `PRIVATE`) e um vínculo opcional genérico `{type, id}` (viagem, item, voo, hotel, restaurante, lugar ou transfer).

`Version` é a versão de sincronização e sobe a cada mudança de metadata. O `Checksum` identifica o conteúdo. Renomear muda a versão, mas não o checksum, e por isso o cliente sabe que não precisa baixar de novo.

**Port `Storage`** (no consumidor), com adapter GridFS. Os "links assinados" são URLs da própria API (`/api/v1/storage/{token}`), com token HMAC-SHA256 que carrega expiração, operação, chave, tamanho, checksum, tipo e nome do arquivo.

**Upload**
1. `POST /trips/{id}/documents`: valida papel e limites, cria a metadata `PENDING` e devolve o link de `PUT`.
2. O cliente envia os bytes com `PUT` no link. O servidor exige `Content-Length` igual ao declarado, grava em streaming e confere o SHA-256; se divergir, apaga o arquivo e responde 422 `upload_mismatch`.
3. `POST /trips/{id}/documents/{docId}/complete`: confere o objeto (existência, tamanho, checksum) e passa para `READY`.

`PENDING` nunca aparece no sync nem em listagens de outros usuários. Arquivo não viaja dentro de mutation, então criar documento fica fora do push. O push só cobre renomear, reclassificar, revincular e apagar.

**Download.** `GET /trips/{id}/documents/{docId}/download` valida membership, papel e visibilidade e devolve JSON `{url, method, expiresAt}` com um link de curta duração. A resposta do arquivo usa `nosniff`, `no-store` e `Content-Disposition` com o nome saneado.

**Storage:** a chave é gerada no servidor, nunca vinda do cliente. Um link vale até expirar mesmo se a permissão for revogada, por isso o TTL é curto.

**Limites iniciais:** 25 MB por arquivo e allowlist de MIME (PDF, JPEG, PNG, WebP, HEIC).

**Ciclo de vida:** apagar faz soft delete da metadata e remove o arquivo do GridFS em best-effort. Ainda não existe um sweeper de órfãos (`PENDING` antigos) nem a limpeza dos filhos de uma viagem apagada; ficam como pendências conhecidas.

## 7. Testes por fase

- Regras de negócio primeiro, com fakes escritos à mão, sem framework de mock.
- Integração (build tag `integration`) contra o Mongo local, nunca Atlas de produção.
- Sync: idempotência (inclusive duplicata concorrente), conflito, delete e tombstone, paginação entre coleções, cursor expirado, vazamento entre viagens, papel por mutation, redação para VIEWER.
- Datas: fusos diferentes, DST, duração de voo, dia local versus UTC.
- Documentos: acesso de não-membro, VIEWER, visibilidade `PRIVATE`, upload incompleto, checksum divergente.

## 8. Decisões em aberto (defaults assumidos)

1. Sync na fase 5, antes de Place, Restaurant, Flight e Hotel.
2. Cursor por `seq` por viagem, com transações e Compose em replica set.
3. Conflito por versão devolvido ao cliente, sem LWW por timestamp.
4. Upload por link assinado (`PUT`) mais `complete`, sem arquivo dentro de mutation.
5. `Visibility` em documentos (`PRIVATE` para passaporte).
6. Limites: 25 MB, TTL de URL de 5 minutos, retenção de tombstones de 90 dias.
7. Arquivos no MongoDB (GridFS), sem AWS nem MinIO.
