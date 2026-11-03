/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "1" in the developer portal's build: mockups are links to their source, not frames. */
  readonly VITE_WIKI_NO_MOCKUPS?: string;
}
