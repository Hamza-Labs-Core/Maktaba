# The demo library (story 60.09)

A household's library made only of openly licensed films, for the screenshots on maktaba.tv
and anywhere else an app is shown: every app shows the same library, in English and Arabic.

- **No person's data.** The household is "The Haddad family" («عائلة حداد»), the home is
  "Living room" («الجهاز الرئيسي»), the account is `haddad`. No answer carries a media path,
  a LAN address or an email (`demo-library.test.mjs` scans every string, T03).
- **Every title is credited** (T02): its licence, the credit line its makers ask for, its film
  page and the source of its art.

## Files

| File | What |
|---|---|
| `library.json` | the library: the household, two libraries (Films, Shorts), ten titles with English and Arabic titles and synopses written for the fixture, one series in two seasons, three live channels, a search with English and Arabic matches, the pinned clock (noon UTC) |
| `art/` | each title's poster (240×360, cropped) and backdrop (640×360), JPEG without metadata |
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
- Apple's `MockHome` (`UITests/DemoShotsUITests.swift` and the tvOS and Mac shots) and
  Android's `FakeHome` (the `Demo*ScreenshotTest`s), from `routes/<lang>/`.

The routes are committed because neither the Xcode nor the Gradle test build runs Node; the
test fails, naming each file, when they are out of step with `library.json`.

## Credits

Films by the Blender Foundation and Blender Studio. Each film is under the licence named below;
the art is each film's card from its page on Blender Studio, under the Creative Commons
Attribution 4.0 licence the studio's terms give its digital content
(https://studio.blender.org/terms-and-conditions/), resized and cropped for this fixture. The
English and Arabic titles and synopses are this fixture's own.

| Title | Arabic title (ours) | Licence | Credit | Sources | Art |
|---|---|---|---|---|---|
| Sintel (2010) | «سينتل» | CC BY 3.0 | © copyright Blender Foundation \| durian.blender.org | [film page](https://studio.blender.org/films/sintel/) · [card](https://studio.blender.org/files/public/thumbnail/03/f7/03f7b0d15c55b428680d4c6bb313f742_m.webp) | `sintel-poster.jpg`, `sintel-backdrop.jpg` |
| Big Buck Bunny (2008) | «الأرنب الكبير» | CC BY 3.0 | © copyright 2008, Blender Foundation \| www.bigbuckbunny.org | [film page](https://studio.blender.org/films/big-buck-bunny/) · [card](https://studio.blender.org/files/public/thumbnail/92/96/9296b1255fff3ef0e25da43f70616349_m.webp) | `big-buck-bunny-poster.jpg`, `big-buck-bunny-backdrop.jpg` |
| Spring (2019) | «الربيع» | CC BY 4.0 | Blender Studio, studio.blender.org | [film page](https://studio.blender.org/films/spring/) · [card](https://studio.blender.org/files/public/thumbnail/ea/65/ea65feaacf95911f39844e2ea97e68b5_m.webp) | `spring-poster.jpg`, `spring-backdrop.jpg` |
| Tears of Steel (2012) | «دموع من فولاذ» | CC BY 3.0 | © copyright Blender Foundation \| mango.blender.org | [film page](https://studio.blender.org/films/tears-of-steel/) · [card](https://studio.blender.org/files/public/thumbnail/3c/83/3c833d978e7de4b58eaa1a12be541664_m.webp) | `tears-of-steel-poster.jpg`, `tears-of-steel-backdrop.jpg` |
| Cosmos Laundromat (2015) | «مغسلة الكون» | CC BY 4.0 | Blender Institute, gooseberry.blender.org | [film page](https://studio.blender.org/films/cosmos-laundromat/) · [card](https://studio.blender.org/files/public/thumbnail/75/54/7554352c0ec78a8540a8e5f3b3671927_m.webp) | `cosmos-laundromat-poster.jpg`, `cosmos-laundromat-backdrop.jpg` |
| Elephants Dream (2006) | «حلم الفيلة» | CC BY 2.5 | © copyright 2006, Blender Foundation, Netherlands Media Art Institute \| www.elephantsdream.org | [film page](https://studio.blender.org/films/elephants-dream/) · [card](https://studio.blender.org/files/public/thumbnail/04/77/0477dfea1d88a94ba1a75f99f433b8f6_m.webp) | `elephants-dream-poster.jpg`, `elephants-dream-backdrop.jpg` |
| Caminandes: Gran Dillama (2013) | «كاميناندس: اللاما الكبير» | CC BY 3.0 | Blender Foundation, caminandes.com | [film page](https://studio.blender.org/films/caminandes-2/) · [card](https://studio.blender.org/files/public/thumbnail/80/4b/804beb324d82e98db44d8308d9e6f495_m.webp) | `gran-dillama-poster.jpg`, `gran-dillama-backdrop.jpg` |
| Caminandes: Llamigos (2016) | «كاميناندس: صديقا اللاما» | CC BY 4.0 | Blender Studio, studio.blender.org | [film page](https://studio.blender.org/films/caminandes-3/) · [card](https://studio.blender.org/files/public/thumbnail/6e/84/6e84fd08dda59d66aeb2152cb879f160_m.webp) | `llamigos-poster.jpg`, `llamigos-backdrop.jpg` |
| Coffee Run (2020) | «مشوار القهوة» | CC BY 4.0 | Blender Studio, studio.blender.org | [film page](https://studio.blender.org/films/coffee-run/) · [card](https://studio.blender.org/files/public/thumbnail/7a/43/7a43fa6b0bc44e62f1cbb21473800e07_m.webp) | `coffee-run-poster.jpg`, `coffee-run-backdrop.jpg` |
| Sprite Fright (2021) | «رعب الجنيات» | CC BY 4.0 | Blender Studio, studio.blender.org | [film page](https://studio.blender.org/films/sprite-fright/) · [card](https://studio.blender.org/files/public/thumbnail/ce/83/ce833b1d7d5862f1c6e64f7d5b8b0fc9_m.webp) | `sprite-fright-poster.jpg`, `sprite-fright-backdrop.jpg` |

The series "Open Shorts" («حكايات قصيرة») is this fixture's grouping of four Blender Studio
shorts into two seasons; it is not a series the studio made.
