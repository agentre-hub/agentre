// Compatibility entry point for host consumers retained after the engine UI move.
export {
  buildPickerCatalog,
  isNativeTarget,
  ModelTargetPicker,
  providerCompatibleForBackend,
  readRecentTargets,
  recordRecentTarget,
  removeRecentTarget,
  sameTarget,
} from "@agentre-ai/agentre-ui";
export { useModelTargetCatalog } from "./model-target-picker/catalog";
export type {
  ModelTarget,
  ModelTargetPickerProps,
  PickerModel,
  PickerProvider,
  PickerScenario,
} from "@agentre-ai/agentre-ui";
