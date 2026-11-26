### Fixed

- **Text that was cut or backwards on TVs and tablets.** On the big screen in a browser or on the
  desktop, a subtitle left to the player sits one line above the screen's edge; one that says
  where it goes stays there. On Samsung and LG TVs the hints name the remote's OK and Back instead
  of a keyboard's keys. On Apple TV the sign-in page's buttons show their whole words and its QR
  code is whole. On Android TV and Fire TV the cards of Live TV's On now no longer cut an Arabic
  line. In Arabic, the Android apps' "Code ABCD-1234" reads from the right, and an English line in
  search and AI answers keeps its full stop at its end. On a tablet on its side, a series' picture
  is smaller, so its seasons and episodes show without scrolling.

- **Maktaba Remote on iPhone and iPad says the queue's stages, steps and causes in Arabic.** In
  the Arabic Remote the queue showed what the home sends in English: stage names (Transcribe,
  Length), the stage ids under each Needs attention group (`probe`, `transcribe`), a running
  job's step and its state. The Remote now says each stage, step, job state and cause in its
  own words, in English and Arabic, the web's wording; a code it does not know keeps what the
  home sent. The plugin causes have their words too.

- **The Android Remote on a 7" tablet, and in Arabic.** On a window about 600 dp wide (a 7"
  tablet upright, a foldable open flat) the Remote shows one pane at a time, as on a phone: the
  list sits beside the page only where both fit at their least widths, and the list is never
  narrower than 360 dp. A detail's value no longer wraps letter by letter beside a long label: it
  goes under the label when a word of it would not fit. In Arabic these are in the Remote's own
  words, the web's where it has them: every processing stage's name (in Needs attention, the
  queue and its running jobs), every failure cause (the plugins' too, and a stage's failing
  cause), a running job's step and what it works on, the update channel and quiet window, how a
  machine is installed and what installed its last update. A failed job's own message still shows
  as the home sent it, and so does a stage or cause this app has no words for. An update's steps
  no longer start with the home's host id, and Needs attention's page no longer repeats its cause
  as a code.

- **A running job's step reads the same everywhere.** The web's queue shows it in words, in English
  and Arabic, in place of the code the home sends (`embed`, `fan_out`), and both Remotes say the
  same words.
