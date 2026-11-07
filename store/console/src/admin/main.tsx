import { createRoot } from "react-dom/client";
import { Review } from "./Review";
import "../styles.css";

createRoot(document.getElementById("root")!).render(<Review initialLang={navigator.language?.startsWith("ar") ? "ar" : "en"} />);
