// maktaba.tv/install/synology's entry (vite.install.config.ts builds it): the published compose
// file is built in, so the page needs nothing from anywhere.

import base from "../../../../cloud/installers/docker/docker-compose.yml?raw";
import { mount } from "./synologyPage";

const root = document.getElementById("guide");
if (root) mount(root, base, document.documentElement.lang === "ar" ? "ar" : "en");
