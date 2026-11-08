// maktaba-admin-web: admin.maktaba.tv (Epic 50 story 50.32 §3). The rules live in site.ts and
// ../../cloud-web/src/edge.ts; this module may export only its handler.
import { handle, type Env } from "../../cloud-web/src/edge";
import { ADMIN_SITE } from "./site";

const serve = handle(ADMIN_SITE, (req) => fetch(req));

export default {
  fetch: (req: Request, env: Env): Promise<Response> => serve(req, env),
};
