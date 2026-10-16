import { createRoot } from "react-dom/client";
import App from "./App";
import Uninstall from "./pages/settings/Uninstall";
import "./app.css";

createRoot(document.getElementById("app")!).render(
  new URLSearchParams(window.location.search).get("view") === "uninstall" ? (
    <Uninstall />
  ) : (
    <App />
  ),
);
