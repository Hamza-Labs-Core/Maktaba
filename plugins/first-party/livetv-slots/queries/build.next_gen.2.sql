-- name: build.next_gen.2
-- mode: write
-- scope: catalog
-- tables: tv_sched_gen_seq*
INSERT INTO tv_sched_gen_seq (at) VALUES ($1)
