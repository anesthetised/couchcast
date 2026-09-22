/* @refresh reload */
import { render } from "solid-js/web";

import App from "./App";
import { registerServiceWorker } from "./lib/install";
import "./styles.css";

registerServiceWorker();

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root element");
}

render(() => <App />, root);
