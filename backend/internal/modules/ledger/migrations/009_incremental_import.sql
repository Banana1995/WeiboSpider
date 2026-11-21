-- Keep the initialization and every frozen receipt. Subsequent distinct exports
-- have their own manifests; repeating an export reuses its existing batch.
DROP INDEX audit_single_account_import;
CREATE UNIQUE INDEX audit_initial_account_import ON audit_log(account_id)
    WHERE entity_type='import' AND action='initialize';
CREATE UNIQUE INDEX audit_account_import_digest ON audit_log(account_id, json_extract(metadata_json,'$.digest'))
    WHERE entity_type='import';
