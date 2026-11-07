# The isolated runner as a Cloudflare Container (story 54.16 §1, #756): the store's
# CheckRunner Durable Object starts it per submission with outbound internet off, no Worker
# secret and no binding, hands it the package and source on port 8080 (serve.py), and gets
# run.sh's report back (RUNNER = "containers", store/README.md). The CI fallback
# (store-check-runner.yml) runs the same run.sh.
FROM rust:1.97-bookworm AS mkp
WORKDIR /src
COPY plugind /src/plugind
RUN cd plugind && cargo build --locked --release -p mkp

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends clamav zstd python3 ca-certificates && rm -rf /var/lib/apt/lists/* \
    && freshclam --quiet || true
COPY --from=mkp /src/plugind/target/release/mkp /usr/local/bin/mkp
COPY store/check-runner /runner
RUN useradd --system --uid 10001 runner
USER runner
EXPOSE 8080
ENTRYPOINT ["python3", "/runner/serve.py", "8080"]
