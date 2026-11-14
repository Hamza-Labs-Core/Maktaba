// Epic 58 story 58.14: the blocked page and the time's-up screen another surface draws in
// their place. The big screen (/big, and the TV build at /tv/) gives its own 10-foot pages,
// laid out for a remote: no parent PIN on a TV (a guardian answers from their phone), and
// the line that says so. Every page that shows Epic 57's screens (the player, a video's
// page) then draws the big screen's inside it, with nothing else changed.
import { createContext, useContext, type ComponentType } from "react";
import type { BlockedProps } from "./AskParent";
import type { TimesUpProps } from "./TimeState";

export interface ParentalScreenSet {
  Blocked?: ComponentType<BlockedProps>;
  TimesUp?: ComponentType<TimesUpProps>;
}

export const ParentalScreens = createContext<ParentalScreenSet>({});

/** The screens this surface draws in place of Epic 57's own (none: the web's). */
export function useParentalScreens(): ParentalScreenSet {
  return useContext(ParentalScreens);
}
