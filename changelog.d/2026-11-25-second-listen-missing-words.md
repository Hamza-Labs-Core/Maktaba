### Fixed

- **A few sentences without word timings no longer keep a video from its second listen.** One
  such sentence left the whole transcript as Whisper heard it, and its tries ran out. The second
  listen now corrects the other sentences. Videos stopped this way come back by themselves after
  the update, a few at a time after the new files, without transcribing again unless their words
  are gone. Whisper's words also stay with their own sentence where one is only punctuation.
