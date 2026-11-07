-- name: vocabulary.prompt_terms
-- mode: read
-- scope: library $1
-- tables: vocabulary
SELECT term FROM vocabulary WHERE kind IN ('name', 'term') AND (scope = 'home' OR library_id = $1) ORDER BY uses DESC, term LIMIT $2
