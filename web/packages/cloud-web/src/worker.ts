// maktaba-cloud-web: app.maktaba.tv (Epic 50 story 50.32 §3). The rules live in site.ts and
// edge.ts; this module may export only its handler.
import { handle, type Env } from "./edge";
import { APP_SITE } from "./site";

const serve = handle(APP_SITE, (req) => fetch(req));

export default {
  fetch: (req: Request, env: Env): Promise<Response> => serve(req, env),
};
