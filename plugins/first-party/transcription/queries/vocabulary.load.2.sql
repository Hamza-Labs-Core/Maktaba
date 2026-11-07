-- name: vocabulary.load.2
-- mode: read
-- scope: library $1
-- tables: vocab_pairs
SELECT language, heard_key, meant, uses FROM vocab_pairs WHERE library_id = $1 OR library_id IS NULL ORDER BY uses DESC, meant
