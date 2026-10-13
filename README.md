# scrapperbook

Monitor pessoal de anúncios do Facebook Marketplace.

Design: `docs/superpowers/specs/2026-10-09-marketplace-monitor-mvp-design.md`

## Rodar

    cp .env.example .env   # edite as senhas
    docker compose up -d --build

## Testes do backend

Requer Docker em execução.

    cd backend && go test ./...
