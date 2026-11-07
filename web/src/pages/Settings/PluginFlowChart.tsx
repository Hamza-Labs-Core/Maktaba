// Epic 54 (story 54.41): a flow drawn as a chart, from the plugin SDK's one layout
// (@maktaba/flowchart). The chart is drawn with React's own SVG elements from geometry and
// plain strings, never as markup it was sent; beside it, the same steps as a list for
// screen readers. Core events are in Maktaba's words; plugin and event names are data.
import { chartModel, flowSteps, nodeClass, CHART_STYLE, type ChartGraph } from "@maktaba/flowchart";
import { useI18n } from "../../lib/i18n";

export function PluginFlowChart({
  graph,
  highlight,
  names,
  title,
}: {
  graph: ChartGraph;
  highlight?: string;
  names?: (id: string) => string;
  title: string;
}) {
  const { t, locale } = useI18n();
  const coreLabel = (event: string) => {
    const key = `plugins.flow.event.${event}`;
    const w = t(key);
    return w === key ? event : w;
  };
  const opts = {
    highlight,
    lang: locale,
    coreLabel,
    pluginName: names,
    missingWord: t("plugins.flow.missing"),
    offWord: t("plugins.flow.off"),
    title,
  };
  if (graph.nodes.length === 0) return <p>{t("plugins.flow.empty")}</p>;
  const m = chartModel(graph, opts);
  const steps = flowSteps(graph, opts);
  return (
    <figure className="mkt-plugins__flow" data-testid="flow-chart">
      <div className="mkt-plugins__flow-scroll">
        <svg
          className="flowchart"
          viewBox={`0 0 ${m.width} ${m.height}`}
          width={m.width}
          height={m.height}
          role="img"
          aria-label={title}
          direction={m.rtl ? "rtl" : "ltr"}
        >
          <style>{CHART_STYLE}</style>
          <defs>
            <marker
              id="fc-a"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="8"
              markerHeight="8"
              orient="auto-start-reverse"
            >
              <path className="fc-arrow" d="M0 0 L8 4 L0 8 z" />
            </marker>
          </defs>
          {m.edges.map((e) => (
            <path
              key={e.key}
              className={`fc-edge fc-${e.kind}${e.off ? " fc-off" : ""}`}
              d={e.d}
              markerEnd="url(#fc-a)"
            />
          ))}
          {m.nodes.map((n) => (
            <g key={n.id} className={nodeClass(n)} data-id={n.id}>
              <rect className="fc-box" x={n.x} y={n.y} width={n.w} height={n.h} rx={n.rx} />
              <text className="fc-main" x={n.tx} y={n.y + 18} textAnchor={n.anchor}>
                {n.main}
              </text>
              <text className="fc-sub" x={n.tx} y={n.y + 34} textAnchor={n.anchor}>
                {n.sub}
              </text>
            </g>
          ))}
        </svg>
      </div>
      <figcaption>
        <ol className="mkt-plugins__flow-steps" aria-label={t("plugins.flow.steps")}>
          {steps.map((s, i) => (
            <li key={i} dir="auto">
              {s}
            </li>
          ))}
        </ol>
      </figcaption>
    </figure>
  );
}
