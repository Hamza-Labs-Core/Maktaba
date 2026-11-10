// The page's entry (vite.config.ts builds it): the published compose file is built in,
// so the page needs nothing from anywhere.

import base from "../../../../cloud/installers/docker/docker-compose.yml?raw";
import { mount } from "./page";

const root = document.getElementById("app");
if (root) mount(root, base, document.documentElement.lang === "ar" ? "ar" : "en");
