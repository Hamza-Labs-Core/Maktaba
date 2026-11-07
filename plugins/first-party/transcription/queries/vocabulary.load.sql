-- name: vocabulary.load
-- mode: read
-- scope: library $1
-- tables: vocabulary
SELECT language, term, key, kind, variants FROM vocabulary WHERE scope = 'home' OR library_id = $1 ORDER BY scope, term
