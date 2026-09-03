# Kimoox generated client

This directory contains the static Go client generated from the local
compatibility schema at `openapi.json`.

## Source and status

- Documentation: <https://docs.kimoox.com/>
- API base URL: `https://card.kimoox.com`
- The public documentation currently exposes an HTML API reference. It does
  not expose a downloadable official `openapi.json` or `swagger.json` at the
  documented host paths.
- `openapi.json` is therefore a compatibility schema reconstructed from the
  published documentation. It must not be represented as Kimoox's original
  vendor-provided OpenAPI file.

The schema currently covers the 22 documented POST endpoints, including card
BINs, account operations, cardholders, card issuing, card operations, card
queries, balances, verification-code lookup, and transactions. The adapter
uses the subset required by the unified `CardProvider` contract; the generated
client also exposes the remaining documented endpoints for future Kimoox
features.

## Regenerate

From this directory:

```bash
go generate ./...
```

The generator is pinned to `oapi-codegen@v2.4.1` by `generate.go`.

Provider-specific signing, error mapping, status mapping, and sensitive-field
decryption remain in the parent `kimoox` package. Do not move those concerns
into the generated client or into `PaymentService`.
