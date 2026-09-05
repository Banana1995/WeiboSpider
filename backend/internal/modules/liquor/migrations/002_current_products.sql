ALTER TABLE products ADD COLUMN active INTEGER NOT NULL DEFAULT 0 CHECK (active IN (0, 1));

UPDATE products SET active=1 WHERE EXISTS (
    SELECT 1 FROM prices q
    WHERE q.source=products.source AND q.product_id=products.id
        AND q.price_date=(SELECT MAX(price_date) FROM prices WHERE source=products.source)
);
