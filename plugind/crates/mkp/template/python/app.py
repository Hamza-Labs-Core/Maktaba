"""{{id}}: a Maktaba plugin in Python (componentize-py).

It runs in maktaba-plugind's sandbox: no network, no files, only the plugin API calls its
manifest asks for and the admin grants. `print` and file calls fail (the build stubs WASI
out); write to the job's log with `log.write`.
"""

import json

from wit_world import RunOutput
from wit_world import WitWorld as World
from wit_world.imports import job, log, types


class WitWorld(World):
    def run(self, stage: str) -> RunOutput:
        log.write(types.Level.INFO, "hello", json.dumps({"stage": stage}))
        subject = job.job_subject()
        return RunOutput(output_hash=f"h-{subject.id}", summary="{}")
