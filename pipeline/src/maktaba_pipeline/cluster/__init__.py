"""Multi-instance Maktaba, the node side (Epic 31, specs/epics/31-multi-instance).

A machine on the LAN finds the home (:mod:`.discover`), asks to join and shows the
pairing code (:mod:`.join`, :mod:`.crypto`), keeps what the home sent in its node config
(:mod:`.config`), and then runs as a worker on the home's database: it reports what it
can do (:mod:`.capabilities`), works out where the shared storage is mounted
(:mod:`.storage`) and claims the stages it may serve (:mod:`.stages`).
"""
