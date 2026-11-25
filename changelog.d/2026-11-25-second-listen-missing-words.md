### Fixed

- **A sentence without word timings no longer keeps a video from its second listen.** One such
  sentence left the whole transcript as Whisper heard it. The words of each sentence now stay with
  it where a sentence is punctuation alone or begins without a space, and the second listen leaves
  alone a sentence whose words are not all there, so it loses, doubles or moves none. The videos
  stopped this way are transcribed again by themselves after the update, once, a few at a time
  after the new files. Fixes made to their words are carried over as for any video transcribed
  again.
