// Types mirroring docs/wiki/db/wiki-schema.json.

export type EntryType =
  | 'epic'
  | 'story'
  | 'plan'
  | 'diagram'
  | 'review'
  | 'mockup'
  | 'endpoint'
  | 'entity'
  | 'feature'
  | 'phase';

export interface WikiEntry {
  id: string;
  type: EntryType;
  title: string;
  epic?: string | null;
  phase?: number | null;
  engine?: string | null;
  content?: string;
  tags?: string[];
  related?: string[];
  files?: Record<string, string | string[] | null>;
  linear?: string | null;
  api_endpoints?: string[];
  db_tables?: string[];
  metadata?: Record<string, unknown> & {
    method?: string;
    path?: string;
    tag?: string;
    operationId?: string;
    story_count?: number;
    plan_count?: number;
    mockup_count?: number;
    category?: string;
    slug?: string;
  };
}

export interface WikiDB {
  version?: string;
  project?: string;
  description?: string;
  generated_from?: string[];
  counts?: Partial<Record<EntryType, number>>;
  entries: WikiEntry[];
}

export interface WikiBundle {
  meta: {
    version: string;
    project: string;
    description: string;
    counts: Partial<Record<EntryType, number>>;
    generated_from: string[];
  };
  entries: WikiEntry[];
  byId: Map<string, WikiEntry>;
  byType: Map<EntryType, WikiEntry[]>;
  epicStories: Map<string, WikiEntry[]>;
  epicPlans: Map<string, WikiEntry[]>;
}

export interface SearchHit {
  entry: WikiEntry;
  score: number;
}
