import React from "react";
import { createRoot } from "react-dom/client";

import App from "./App";
import "./i18n";
import { installNativeClipboard } from "./lib/native-clipboard";
import "./styles/globals.css";

installNativeClipboard();

const container = document.getElementById("root");

if (!container) {
  throw new Error("root element not found");
}

const root = createRoot(container);

root.render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
