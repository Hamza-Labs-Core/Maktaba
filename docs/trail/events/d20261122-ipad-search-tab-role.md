---
schema: 1
kind: incident
title: "iPadOS 27.2: the search-role tab resets on focus, and a submitted search's field never takes the keyboard again"
date: 2026-11-22
session: f531cea2-971f-4c9c-833d-47dc53bdf492
---

## What happened
- On #1486 (Xcode 27), `PlayerSearchUITests.testATitleTappedWhileRefiningLetsTheKeyboardGoAndOpensItsPage`
  failed on the iPad in every CI run on the xcode-27 image: after a submitted search, a tap
  on the search field gave it no keyboard. iPhone 27.2 and iPad 27.0 passed.
- Six diagnostic rounds ran on the scratch branch `diag/ipad-search-272` (never merged). They
  ruled out:
  - the simulator's hardware keyboard;
  - the app's own focus drop and its `.searchFocused` binding (round 4, run 44690036232);
  - the search suggestions;
  - an `isPresented` binding;
  - the results list's scroll edge;
  - the tab customization.

  A field holding no text did take the keyboard.
- Round 5 logged the field's begin and end of editing with the call stack. A tap began
  editing and ended it about 0.5 s later. The end came from
  `UIKitAdaptableTabView.setTabs` → `UITabBarController setTabs:animated:` →
  `setSelectedIndex` → the Search tab's `viewWillDisappear` → `resignFirstResponder`. On a
  first tap the search bar's activation animation gave the field the keyboard back. With a
  search already submitted the search was already active, and nothing did.
- Round 6 (run 44997129152): without the `.search` role on the Search tab, both the
  diagnostic and the real test passed. `tabViewSearchActivation` and dropping the
  customization did not help. In 92a6879b3 the Search tab is an ordinary tab on an iPad
  (`PlayerShell.role`); the iPhone and the Mac keep the search role.

## What we learned
- On iPadOS 27.2, SwiftUI's `.sidebarAdaptable` TabView sets its UIKit tabs again when a
  `Tab(role: .search)`'s field takes focus, and the tab's screen loses first responder.
- To find who takes focus away, observe `UITextField.textDidEndEditingNotification` with
  `queue: nil` and log `Thread.callStackSymbols`. One run named the caller after four rounds
  of guessing by removal.

## Keep doing
- Diagnostic legs in parallel on a scratch branch, each one change, with the real test
  kept unchanged.

## Stop doing
- Removing app code one piece per round before asking the system who did it.

## Open
- Whether a later iPadOS fixes the search-role tab reset. The role could then come back on
  the iPad.
