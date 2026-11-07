# Runbook: the cloud's errors spike, or a node's logs stop reaching the witness

> Alerts `cloud_error_spike`, `log_forward_lagging` (Epic 50 story 50.10, Epic 53.17).

- **`cloud_error_spike`**: the error groups in the service admin's cloud log view (Epic 53.17) say
  which code path; a deploy within the last hour is the first suspect (roll it back:
  `sudo bluegreen <the previous tag>` on the leader).
- **`log_forward_lagging`**: the node's forwarder cannot reach the witness (the private network,
  the witness's disk). Its logs stay on the node (14 days) and are sent when it can again.
