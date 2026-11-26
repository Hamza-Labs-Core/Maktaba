// A processing stage's title in the reader's language. The API sends each stage's label
// as the pipeline graph names it (engine/graph.py, in English); the words for a stage are
// under queue.stage.<stage>.title, and a stage with no words yet (a newer pipeline's, an
// extension's) keeps the label it came with.
import { useMemo } from "react";
import { useI18n } from "./i18n";
import type { QueuesSnapshot, StageInfo } from "./queueApi";

type T = (key: string, vars?: Record<string, string | number>) => string;

/** The stage's title: its words when the catalog has them, else the label the API sent. */
export function stageTitle(t: T, stage: string, label: string): string {
  const key = `queue.stage.${stage}.title`;
  const words = t(key);
  return words === key ? label : words;
}

/**
 * A running job's step (`ctx.progress("<step>", ...)`) in the reader's language: its words under
 * queue.running.step.<step>, a stage's title when a stage reports a stage's name, else as the
 * home sent it, its underscores as spaces. A step named with spaces is keyed with underscores.
 */
export function stepTitle(t: T, step: string): string {
  const code = step.trim().replaceAll(" ", "_");
  const key = `queue.running.step.${code}`;
  const words = t(key);
  return words === key ? stageTitle(t, code, code.replaceAll("_", " ")) : words;
}

/** The stage with its label in the reader's language. */
export function titledStage(t: T, s: StageInfo): StageInfo {
  const label = stageTitle(t, s.stage, s.label);
  return label === s.label ? s : { ...s, label };
}

/** Each stage with its label in the reader's language. */
export function titledStages(t: T, stages: StageInfo[]): StageInfo[] {
  return stages.map((s) => titledStage(t, s));
}

/** The snapshot with its stages' labels in the reader's language. */
export function titledSnapshot(t: T, s: QueuesSnapshot | null): QueuesSnapshot | null {
  if (!s || !Array.isArray(s.stages)) return s;
  return { ...s, stages: titledStages(t, s.stages) };
}

/** titledStages, kept until the stages or the language change. */
export function useTitledStages(stages: StageInfo[]): StageInfo[] {
  const { t } = useI18n();
  return useMemo(() => titledStages(t, stages), [t, stages]);
}
