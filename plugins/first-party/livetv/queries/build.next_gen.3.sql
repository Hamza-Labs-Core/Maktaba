-- name: build.next_gen.3
-- mode: read
-- scope: catalog
-- tables: tv_sched_gen_seq
SELECT MAX(gen) FROM tv_sched_gen_seq
