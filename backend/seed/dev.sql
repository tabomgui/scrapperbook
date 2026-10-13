-- Exemplo do MVP: "Honda Civic EG" em Curitiba/PR a cada 30 minutos.
-- fb_location 'curitiba' é provisório; o plano 2 confirma o slug real do Facebook.
INSERT INTO search_regions (id, name, kind, city, state, lat, lng, radius_km, fb_location)
VALUES ('curitiba-pr', 'Curitiba/PR', 'city', 'Curitiba', 'PR', -25.4284, -49.2733, 60, 'curitiba')
ON CONFLICT (id) DO NOTHING;

INSERT INTO search_definitions (id, name, keyword, frequency, filters, alert)
VALUES ('00000000-0000-0000-0000-00000000c1c1', 'Civic EG', 'Honda Civic EG', '30 minutes',
        '{"max_price": 30000}',
        '{"enabled": true, "on_new": true, "on_price_drop": true, "max_price": 30000}')
ON CONFLICT (id) DO NOTHING;

INSERT INTO search_definition_regions (search_id, region_id)
VALUES ('00000000-0000-0000-0000-00000000c1c1', 'curitiba-pr')
ON CONFLICT DO NOTHING;
