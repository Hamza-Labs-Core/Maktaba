import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";
import type { ClusterLink, ClusterNode } from "../../lib/clusterApi";
import { LinkCard, fingerprint } from "./LinkCard";

const link: ClusterLink = {
  mode: "prefer",
  auto_require: true,
  ca_sha256: "ab12cd34",
  ca_url: "/api/cluster/ca.crt",
  web_https: false,
  ready: 1,
  machines: 2,
  nodes: {
    n1: {
      connected: true,
      has_cert: true,
      cert_not_after: new Date(Date.now() + 86_400_000 * 20).toISOString(),
    },
    n2: { connected: false, has_cert: false },
  },
};

describe("LinkCard", () => {
  it("shows the mode, the machines on the link, the CA's fingerprint and its download", () => {
    const nodes = [
      { id: "n1", name: "studio" },
      { id: "n2", name: "lounge" },
    ] as ClusterNode[];
    render(
      <I18nProvider>
        <LinkCard link={link} nodes={nodes} />
      </I18nProvider>
    );
    expect(screen.getByText(/1 of 2 machines/)).toBeInTheDocument();
    expect(screen.getByText("studio")).toBeInTheDocument();
    expect(screen.getByText("AB:12:CD:34")).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute("href", "/api/cluster/ca.crt");
  });

  it("says, while the link is required, which machine's database login keeps its password", () => {
    const nodes = [
      { id: "n1", name: "studio" },
      { id: "n2", name: "lounge" },
    ] as ClusterNode[];
    const later = new Date(Date.now() + 86_400_000 * 20).toISOString();
    render(
      <I18nProvider>
        <LinkCard
          link={{
            ...link,
            mode: "require",
            nodes: {
              n1: { connected: true, has_cert: true, cert_not_after: later, db_cert: true },
              n2: { connected: true, has_cert: true, cert_not_after: later },
            },
          }}
          nodes={nodes}
        />
      </I18nProvider>
    );
    const said = screen.getAllByText(/database login still uses its password alone/);
    expect(said).toHaveLength(1);
    expect(said[0].closest("li")).toHaveTextContent("lounge");
  });

  it("groups a fingerprint in pairs", () => {
    expect(fingerprint("0a1b2c")).toBe("0A:1B:2C");
  });
});
