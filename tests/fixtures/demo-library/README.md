# The demo library (story 60.09)

A household's library made only of openly licensed films, for the screenshots on maktaba.tv
and anywhere else an app is shown: every app shows the same library, in English and Arabic.

- **No woman or girl, anywhere.** The titles are documentaries with no people in them (space,
  the Earth seen from orbit, the deep sea, a rocket launch filmed from its tower) and children's
  animation whose characters are animals (Big Buck Bunny, the Caminandes llama films). Each film
  was looked through frame by frame on a contact sheet and each picture in `art/` and the
  player's still checked by eye; a title that cannot be checked is replaced, not kept. A test
  scans every title, synopsis, channel and line for words that name or describe a woman or girl.
- **Family viewing.** Nothing a conservative household would not show: no music videos, no
  romance, no frightening scenes.

- **No person's data.** The household is "The Haddad family" («عائلة حداد»), the home is
  "Living room" («الجهاز الرئيسي»), the account is `haddad`. No answer carries a media path,
  a LAN address or an email (`demo-library.test.mjs` scans every string, T03).
- **Every title is credited** (T02): its licence, the credit line its makers ask for, its film
  page and the source of its art.

## Files

| File | What |
|---|---|
| `library.json` | the library: the household, two libraries (Documentaries, Kids), ten titles with English and Arabic titles and synopses written for the fixture, each with its licence, credit, source and where its art came from, one series in two seasons, three live channels, a search with English and Arabic matches, the pinned clock (noon UTC) |
| `art/` | each title's poster (240×360, cropped) and backdrop (640×360), JPEG without metadata: a frame of the film, or a Blender Studio film's card |
| `routes.mjs` | turns `library.json` into what a home answers, per language, in MockHome's route-table format |
| `routes/en/`, `routes/ar/` | its output: `routes.json` (method, path, status, `file` or, for an image, `art`) and one file per answer. Generated: never edit by hand |
| `demo-library.test.mjs` | T02, T03, and `routes/` in step with `library.json` |

```sh
node tests/fixtures/demo-library/routes.mjs            # after changing library.json
node --test tests/fixtures/demo-library/demo-library.test.mjs                # the tests (CI: website / Tests (the demo library))
```

Times in the answers are `{{now}}`, `{{now-N}}`, `{{now+N}}` (seconds), as in Apple's
`UITests/Fixtures`. Each reader fills them from `library.json`'s `clock`, so a screenshot
taken on any day shows the same dates, guide and progress.

**Who reads it:**

- the web capture, `web/packages/website/scripts/capture-web.mjs` (`buildRoutes`, answered
  through Playwright's routing);
- Apple's `MockHome` (its demo mode, for the iPhone, iPad, Apple TV and Mac shots), from
  `routes/<lang>/`;
- Android: `DemoLibrary` in `:client`'s test fixtures reads `routes/<lang>/` for the phone's
  `FakeHome` and the TV app's tests (`DemoScreenshotTest` in `apps/android/player` and
  `apps/tv/android/app`), whose Roborazzi references are under `src/test/screenshots/demo/`.

The routes are committed because neither the Xcode nor the Gradle test build runs Node; the
test fails, naming each file, when they are out of step with `library.json`.

## Credits

Each film is under the licence named below, and its page (the film page) is where that licence
was read: Wikimedia Commons' file page, with its licence and source, or Blender Studio's film
page. The art is a frame of the film itself, or, for Big Buck Bunny, Gran Dillama and Llamigos,
the film's card from its page on Blender Studio, under the Creative Commons Attribution 4.0
licence the studio's terms give its digital content
(https://studio.blender.org/terms-and-conditions/); each is resized and cropped for this
fixture. The English and Arabic titles and synopses, the search's lines and the player's
subtitle line are this fixture's own.

Works of NASA (and of NOAA, a United States agency) are in the public domain in the United
States; NASA asks that its material not be used to imply its endorsement, and nothing here does.
Their emblems are not part of that: the ghost octopus's frame carries NOAA's and the ship's name
in its corner, and its art here is cropped clear of both.
Gran Dillama is under CC BY-SA 3.0: its art here is the studio's card (CC BY 4.0), not a frame
of the film.

| Title | Arabic title (ours) | Licence | Credit | Film page | Art |
|---|---|---|---|---|---|
| Curiosity: Landing on Mars (2011) | «كيوريوسيتي: الهبوط على المريخ» | Public domain | NASA/JPL-Caltech | [film page](<https://commons.wikimedia.org/wiki/File:Mars_Science_Laboratory_Curiosity_Rover_Animation.webm>) | a frame at 7:00, Public domain: [source](<https://commons.wikimedia.org/wiki/File:Mars_Science_Laboratory_Curiosity_Rover_Animation.webm>) · `curiosity-poster.jpg`, `curiosity-backdrop.jpg` |
| Apollo 11: The Launch (1969) | «أبولو 11: الإقلاع» | Public domain | NASA/Kennedy Space Center | [film page](<https://commons.wikimedia.org/wiki/File:Apollo_11_launch,_video_of_engines_at_500_fps_(camera_E-8).ogv>) | a frame at 1:00, Public domain: [source](<https://commons.wikimedia.org/wiki/File:Apollo_11_launch,_video_of_engines_at_500_fps_(camera_E-8).ogv>) · `apollo-11-launch-poster.jpg`, `apollo-11-launch-backdrop.jpg` |
| Along the Rings of Saturn (2018) | «على امتداد حلقات زحل» | CC BY 2.0 | NASA/JPL-Caltech/SSI/CICLOPS/Kevin M. Gill | [film page](<https://commons.wikimedia.org/wiki/File:Along_the_Rings_of_Saturn_(43738250680).webm>) | a frame at 0:10, CC BY 2.0: [source](<https://commons.wikimedia.org/wiki/File:Along_the_Rings_of_Saturn_(43738250680).webm>) · `saturn-rings-poster.jpg`, `saturn-rings-backdrop.jpg` |
| Lake Nasser from Space (2025) | «بحيرة ناصر من الفضاء» | Public domain | NASA, Landsat | [film page](<https://commons.wikimedia.org/wiki/File:Lake_Nassar_Time_Series_(Landsat).webm>) | a frame at 0:40, Public domain: [source](<https://commons.wikimedia.org/wiki/File:Lake_Nassar_Time_Series_(Landsat).webm>) · `lake-nasser-poster.jpg`, `lake-nasser-backdrop.jpg` |
| The Moon's Phases, 2023 (2022) | «أطوار القمر 2023» | Public domain | NASA's Scientific Visualization Studio | [film page](<https://commons.wikimedia.org/wiki/File:Moon_Phase_and_Libration,_2023_(SVS5048_-_phases_2023_plain_720p30).webm>) | a frame at 0:30, Public domain: [source](<https://commons.wikimedia.org/wiki/File:Moon_Phase_and_Libration,_2023_(SVS5048_-_phases_2023_plain_720p30).webm>) · `moon-phases-poster.jpg`, `moon-phases-backdrop.jpg` |
| A Ghost Octopus in the Deep (2016) | «أخطبوط شبحي في الأعماق» | Public domain | NOAA Office of Ocean Exploration and Research | [film page](<https://commons.wikimedia.org/wiki/File:NEW_Octopus_Species-_2016_Hohonu_Moana-_Exploring_Deep_Waters.webm>) | a frame at 0:22, Public domain: [source](<https://commons.wikimedia.org/wiki/File:NEW_Octopus_Species-_2016_Hohonu_Moana-_Exploring_Deep_Waters.webm>) · `ghost-octopus-poster.jpg`, `ghost-octopus-backdrop.jpg` |
| Big Buck Bunny (2008) | «الأرنب الكبير» | CC BY 3.0 | © copyright 2008, Blender Foundation \| www.bigbuckbunny.org | [film page](<https://studio.blender.org/films/big-buck-bunny/>) | the film's card on Blender Studio, CC BY 4.0: [source](<https://studio.blender.org/files/public/thumbnail/92/96/9296b1255fff3ef0e25da43f70616349_m.webp>) · `big-buck-bunny-poster.jpg`, `big-buck-bunny-backdrop.jpg` |
| Caminandes: Llama Drama (2013) | «كاميناندس: دراما اللاما» | CC BY 3.0 | Blender Foundation, caminandes.com | [film page](<https://commons.wikimedia.org/wiki/File:Caminandes-_Llama_Drama_-_Short_Movie.ogv>) | a frame at 0:18, CC BY 3.0: [source](<https://commons.wikimedia.org/wiki/File:Caminandes-_Llama_Drama_-_Short_Movie.ogv>) · `llama-drama-poster.jpg`, `llama-drama-backdrop.jpg` |
| Caminandes: Gran Dillama (2013) | «كاميناندس: اللاما الكبير» | CC BY-SA 3.0 | Blender Foundation, caminandes.com | [film page](<https://commons.wikimedia.org/wiki/File:Caminandes_-_Gran_Dillama_-_Blender_Foundation's_new_Open_Movie.webm>) | the film's card on Blender Studio, CC BY 4.0: [source](<https://studio.blender.org/files/public/thumbnail/80/4b/804beb324d82e98db44d8308d9e6f495_m.webp>) · `gran-dillama-poster.jpg`, `gran-dillama-backdrop.jpg` |
| Caminandes: Llamigos (2016) | «كاميناندس: صديقا اللاما» | CC BY 4.0 | Blender Studio, studio.blender.org | [film page](<https://studio.blender.org/films/caminandes-3/>) | the film's card on Blender Studio, CC BY 4.0: [source](<https://studio.blender.org/files/public/thumbnail/6e/84/6e84fd08dda59d66aeb2152cb879f160_m.webp>) · `llamigos-poster.jpg`, `llamigos-backdrop.jpg` |

The series "Caminandes" («كاميناندس») is the Blender Foundation's series of llama shorts; its
two seasons here are this fixture's grouping of the three episodes, not the studio's.

**The player's picture** (Apple's `UITests/Fixtures/Stream/demo-picture-*.ts`, story 60.09's
player shots) is a still of Curiosity: Landing on Mars at 7:00 (public domain, NASA/JPL-Caltech),
its brightest highlights held just below white so the subtitle check counts only the line's
letters. Android's TV player shot draws the film's backdrop instead.

**Arabic content.** Every title has an Arabic title and synopsis, and Lake Nasser from Space is
about Egypt's lake on the Nile. No openly licensed Arabic-language documentary that shows no
woman was found and checked for this fixture; one that is would replace a title here.
