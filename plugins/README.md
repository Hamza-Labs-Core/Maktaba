# plugins/

Maktaba's own stages as first-party plugins (Epic 54 Phase 4, README §15, story 54.27).

```
sdk/python/maktaba_plugin_sdk/   the native mode (named statements, the plugin API client, the
                                 values on the wire), the contract tests' fake API, parity helpers
first-party/<name>/              one folder per plugin, added by its wave (stories 54.28-54.36):
  manifest.toml                  the trusted manifest (README §15.5)
  i18n/en.json, ar.json          its strings
  queries/*.sql                  its named statements (tools/plugins/statements_build.py)
  statements.toml                the shared pipeline modules whose statements it runs
  parity.toml                    tables, ignored columns and cases for the parity proof
  src/maktaba_fp_<name>/         its stages, fingerprints and domain code
  tests/                         unit, contract and parity tests
```

Every package here ships inside the pipeline's wheel (`pipeline/hatch_build.py`), the release's
signed Python artifact; the server embeds the bundle of manifests, strings and statements
(`shared/plugins/firstparty.json`, written by `python3 tools/plugins/bundle.py --write`).
The developer guide is `docs/developers/plugins/first-party.md` (maktaba.dev).
