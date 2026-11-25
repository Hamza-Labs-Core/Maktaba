### Fixed

- **An update waits for a machine's copy that really runs.** An automatic update now sees a machine
  copying a table of the home's database, and waits for it, a day at most; before it did not see
  the copy at all, and its restart ended it. Before it applies an update the home waits for a
  running copy once more, ten minutes at most, while the library serves, and Admin → Updates says
  so. A copy still running then is cut off, and the machine copies that table again. The Updates
  page also says why **Update now** or a machine's action was refused in words, in English and
  Arabic.
