// {{id}}: a Maktaba plugin in JavaScript (jco componentize).
//
// It runs in maktaba-plugind's sandbox: no network, no files, only the plugin API calls its
// manifest asks for and the admin grants. Write to the job's log with log.write.
import { jobSubject } from "maktaba:plugin/job@1.2.0";
import { write } from "maktaba:plugin/log@1.2.0";

export function run(stage) {
  write("info", "hello", JSON.stringify({ stage }));
  const subject = jobSubject();
  return { outputHash: `h-${subject.id}`, summary: "{}" };
}
