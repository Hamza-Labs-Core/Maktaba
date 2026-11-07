# The isolated runner as a Cloudflare Container (story 54.16 §1), for when the Containers
# spike (README §14 item 11) confirms it fits: started per check with outbound internet off,
# no Worker secret and no binding, it runs run.sh on the package and source the store hands
# it and returns the report. Until then the CI fallback (store-check-runner.yml) runs the
# same run.sh.
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
ENTRYPOINT ["/runner/run.sh"]
