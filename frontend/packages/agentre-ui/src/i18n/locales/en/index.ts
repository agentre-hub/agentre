// `agentreUi` remains one i18next namespace. These files are physical domain
// modules only, so callers keep using the existing keys without a module prefix.
import chat from "./chat.json";
import common from "./common.json";
import llm from "./llm.json";
import org from "./org.json";
import projects from "./projects.json";
import session from "./session.json";
import transcript from "./transcript.json";

export default {
  ...chat,
  ...common,
  ...llm,
  ...org,
  ...projects,
  ...session,
  ...transcript,
};
