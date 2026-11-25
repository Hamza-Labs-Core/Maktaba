### Fixed

- **An update waits for a machine's copy that really runs.** An automatic update now sees a machine
  copying a table of the home's database, and waits for it, a day at most; before it did not see
  the copy at all, and its restart ended it. Right before its restart the home waits for a running
  copy once more, ten minutes at most, while the library still serves, and Admin → Updates says so.
  Past that, the machine copies the table it was on again. The Updates page also says why
  **Update now** was refused in words, in English and Arabic.
