/* @refresh reload */
import { render } from "solid-js/web";

import App from "./App";
import { installGlobalHandlers } from "./lib/diagnostics";
import { registerServiceWorker } from "./lib/install";
import "./styles.css";

installGlobalHandlers();
registerServiceWorker();

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root element");
}

render(() => <App />, root);
