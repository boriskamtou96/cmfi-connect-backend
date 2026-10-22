-- KPAY-NOW replaces pawaPay. The id of a payment stays ours (sent to KPAY as
-- externalId, like the refund_id of its refund); KPAY gives its own ids,
-- needed to refund and to recognize its webhooks.
ALTER TABLE payments
    ADD COLUMN provider_payment_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN provider_refund_id TEXT NOT NULL DEFAULT '';
