// A Retina (2x) main display for the Mac Player's product screenshots (story 60.14), on a CI
// runner whose own display is 1x (GitHub's macOS runners: 1024 x 768 at 1x, so a window shot
// came out 1024 x 674 and was scaled up, blurred, into the Mac App Store's 2880 x 1800).
//
//   clang -fobjc-arc -framework Foundation -framework CoreGraphics -framework AppKit \
//     tools/apple/hidpi-display.m -o hidpi-display
//   hidpi-display 1920x1200 READY_FILE &     # until it is killed
//
// It makes the main display one of WIDTH x HEIGHT points drawn at 2x: a mode of the runner's
// own display if it has one that large, else a virtual display (CoreGraphics' CGVirtualDisplay,
// as macOS's own Sidecar and AirPlay displays are made; not public API, so only this CI tool
// uses it) made the main one, the menu bar and new windows on it. A window of 1440 x 900
// points there is 2880 x 1800 pixels, its title bar and toolbar drawn by macOS as on any Mac.
// It writes READY_FILE ("<width> <height> <scale>") once the display is the main one, and
// keeps it while it runs: the virtual display goes when the process ends. Exits 1, with what it
// saw, when it cannot.
//
// On GitHub's runner (macOS 27, a virtual machine) a virtual display lists no modes and has no
// current mode (CGDisplayCopyAllDisplayModes is empty), yet it is online and active at the
// size asked for: it is taken by its bounds and its pixels, and AppKit's scale for it.
#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>

@interface CGVirtualDisplayDescriptor : NSObject
@property(retain, nonatomic) NSString *name;
@property(nonatomic) unsigned int maxPixelsHigh;
@property(nonatomic) unsigned int maxPixelsWide;
@property(nonatomic) CGSize sizeInMillimeters;
@property(nonatomic) unsigned int serialNum;
@property(nonatomic) unsigned int productID;
@property(nonatomic) unsigned int vendorID;
@property(copy, nonatomic) void (^terminationHandler)(id, id);
- (void)setDispatchQueue:(dispatch_queue_t)queue;
@end

@interface CGVirtualDisplayMode : NSObject
- (instancetype)initWithWidth:(unsigned int)width height:(unsigned int)height refreshRate:(double)refreshRate;
@end

@interface CGVirtualDisplaySettings : NSObject
@property(retain, nonatomic) NSArray *modes;
@property(nonatomic) unsigned int hiDPI;
@end

@interface CGVirtualDisplay : NSObject
@property(readonly, nonatomic) unsigned int displayID;
- (instancetype)initWithDescriptor:(CGVirtualDisplayDescriptor *)descriptor;
- (BOOL)applySettings:(CGVirtualDisplaySettings *)settings;
@end

static void printModes(CGDirectDisplayID d) {
    NSDictionary *opts = @{(__bridge NSString *)kCGDisplayShowDuplicateLowResolutionModes: @YES};
    NSArray *modes = CFBridgingRelease(CGDisplayCopyAllDisplayModes(d, (__bridge CFDictionaryRef)opts));
    for (id m in modes) {
        CGDisplayModeRef mode = (__bridge CGDisplayModeRef)m;
        fprintf(stderr, "  display %u: %zu x %zu points, %zu x %zu pixels\n", d, CGDisplayModeGetWidth(mode),
                CGDisplayModeGetHeight(mode), CGDisplayModeGetPixelWidth(mode), CGDisplayModeGetPixelHeight(mode));
    }
}

/// The display's smallest 2x mode of at least `w` x `h` points, NULL when it has none.
static CGDisplayModeRef retinaMode(CGDirectDisplayID d, size_t w, size_t h) {
    NSDictionary *opts = @{(__bridge NSString *)kCGDisplayShowDuplicateLowResolutionModes: @YES};
    NSArray *modes = CFBridgingRelease(CGDisplayCopyAllDisplayModes(d, (__bridge CFDictionaryRef)opts));
    CGDisplayModeRef best = NULL;
    for (id m in modes) {
        CGDisplayModeRef mode = (__bridge CGDisplayModeRef)m;
        size_t mw = CGDisplayModeGetWidth(mode), mh = CGDisplayModeGetHeight(mode);
        if (CGDisplayModeGetPixelWidth(mode) != 2 * mw || mw < w || mh < h) continue;
        if (!best || mw < CGDisplayModeGetWidth(best)) best = mode;
    }
    return best ? CGDisplayModeRetain(best) : NULL;
}

/// A tenth of a second, the main queue and AppKit's screen changes served meanwhile.
static void pause_a_little(void) {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, false);
    usleep(100000);
}

/// The virtual display, kept for the life of the process.
static CGVirtualDisplay *kept;

static BOOL online(CGDirectDisplayID d) {
    CGDirectDisplayID ids[16];
    uint32_t n = 0;
    CGGetOnlineDisplayList(16, ids, &n);
    for (uint32_t i = 0; i < n; i++)
        if (ids[i] == d) return YES;
    return NO;
}

/// AppKit's scale for the display (its NSScreen's backingScaleFactor), 0 without a screen.
static CGFloat screenScale(CGDirectDisplayID d) {
    for (NSScreen *s in NSScreen.screens)
        if ([s.deviceDescription[@"NSScreenNumber"] unsignedIntValue] == d) return s.backingScaleFactor;
    return 0;
}

/// The display's size in points, its pixels and AppKit's scale, in one line.
static void describe(const char *what, CGDirectDisplayID d) {
    CGRect b = CGDisplayBounds(d);
    fprintf(stderr, "%s: display %u, online %d, active %d, main %d, %.0f x %.0f points at (%.0f, %.0f), %zu x %zu pixels, scale %.0f\n",
            what, d, online(d), CGDisplayIsActive(d), CGMainDisplayID() == d, b.size.width, b.size.height, b.origin.x,
            b.origin.y, CGDisplayPixelsWide(d), CGDisplayPixelsHigh(d), screenScale(d));
}

/// Whether the display is `w` x `h` points or more, drawn at 2x (by its pixels or AppKit's scale).
static BOOL isRetina(CGDirectDisplayID d, unsigned int w, unsigned int h) {
    CGRect b = CGDisplayBounds(d);
    if (b.size.width < w || b.size.height < h) return NO;
    return CGDisplayPixelsWide(d) == 2 * (size_t)b.size.width || screenScale(d) == 2;
}

/// A virtual display asked for in one of the ways macOS versions take (its modes in pixels or in
/// points, `hiDPI` 1 or 2): its id once it is online, 0 when it was not made.
static CGDirectDisplayID makeVirtual(unsigned int w, unsigned int h, unsigned int hiDPI, NSArray<NSNumber *> *scales,
                                     unsigned int serial) {
    CGVirtualDisplayDescriptor *desc = [[NSClassFromString(@"CGVirtualDisplayDescriptor") alloc] init];
    [desc setDispatchQueue:dispatch_get_main_queue()];
    desc.name = @"Maktaba shots";
    desc.maxPixelsWide = 2 * w;
    desc.maxPixelsHigh = 2 * h;
    // a 16-inch screen's size: 2 * w pixels on it are a Retina density
    desc.sizeInMillimeters = CGSizeMake(345, 345.0 * h / w);
    desc.productID = 0x6d6b + serial;
    desc.vendorID = 0x6d6b;
    desc.serialNum = serial;
    desc.terminationHandler = ^(id a, id b) { fprintf(stderr, "a virtual display ended\n"); };
    CGVirtualDisplay *display = [[NSClassFromString(@"CGVirtualDisplay") alloc] initWithDescriptor:desc];
    if (!display || display.displayID == 0) return 0;
    kept = display;
    CGVirtualDisplaySettings *settings = [[NSClassFromString(@"CGVirtualDisplaySettings") alloc] init];
    settings.hiDPI = hiDPI;
    NSMutableArray *modes = [NSMutableArray array];
    for (NSNumber *s in scales)
        [modes addObject:[[NSClassFromString(@"CGVirtualDisplayMode") alloc] initWithWidth:s.unsignedIntValue * w
                                                                                  height:s.unsignedIntValue * h
                                                                             refreshRate:60]];
    settings.modes = modes;
    BOOL applied = [display applySettings:settings];
    CGDirectDisplayID d = display.displayID;
    for (int i = 0; i < 50 && !online(d); i++) pause_a_little();
    // AppKit hears of the new screen through the run loop
    for (int i = 0; i < 20 && screenScale(d) == 0; i++) pause_a_little();
    fprintf(stderr, "hiDPI %u, modes x%s: settings taken %d\n", hiDPI,
            [[scales componentsJoinedByString:@" x"] UTF8String], applied);
    return d;
}

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        unsigned int w = 0, h = 0;
        if (argc != 3 || sscanf(argv[1], "%ux%u", &w, &h) != 2 || w == 0 || h == 0) {
            fprintf(stderr, "usage: hidpi-display WIDTHxHEIGHT READY_FILE\n");
            return 2;
        }
        NSString *ready = [NSString stringWithUTF8String:argv[2]];
        // a connection to the window server as an app has one, and AppKit's screens
        [NSApplication sharedApplication];
        CGDirectDisplayID first = CGMainDisplayID();
        describe("the runner's display", first);
        printModes(first);

        CGDirectDisplayID target = first;
        CGDisplayModeRef mode = retinaMode(first, w, h);
        if (!mode) {
            // none of its own: a virtual display drawn at 2x, asked for each way in turn
            if (!NSClassFromString(@"CGVirtualDisplayDescriptor")) {
                fprintf(stderr, "this macOS has no CGVirtualDisplay\n");
                return 1;
            }
            struct { unsigned int hiDPI; NSArray<NSNumber *> *scales; } ways[] = {
                {1, @[@2, @1]}, {1, @[@1]}, {2, @[@2]}, {1, @[@2]}};
            BOOL found = NO;
            for (unsigned int k = 0; k < sizeof ways / sizeof *ways && !found; k++) {
                kept = nil;
                for (int i = 0; i < 20; i++) pause_a_little(); // the previous one gone
                CGDirectDisplayID d = makeVirtual(w, h, ways[k].hiDPI, ways[k].scales, k + 1);
                if (!d) {
                    fprintf(stderr, "way %u: the virtual display was not made\n", k);
                    continue;
                }
                for (int i = 0; i < 30 && !(mode = retinaMode(d, w, h)) && !isRetina(d, w, h); i++) pause_a_little();
                describe("the virtual display", d);
                printModes(d);
                if (mode || isRetina(d, w, h)) {
                    target = d;
                    found = YES;
                }
            }
            if (!found) {
                fprintf(stderr, "no virtual display was %u x %u points at 2x\n", w, h);
                return 1;
            }
        }

        CGDisplayConfigRef config;
        CGBeginDisplayConfiguration(&config);
        if (mode) CGConfigureDisplayWithDisplayMode(config, target, mode, NULL);
        if (target != first) {
            // the main display is the one at the origin: the virtual one, the runner's beside it
            CGConfigureDisplayOrigin(config, target, 0, 0);
            CGConfigureDisplayOrigin(config, first, (int32_t)CGDisplayBounds(target).size.width, 0);
        }
        CGError err = CGCompleteDisplayConfiguration(config, kCGConfigureForSession);
        if (mode) CGDisplayModeRelease(mode);
        if (err != kCGErrorSuccess) {
            fprintf(stderr, "the display configuration failed: %d\n", err);
            return 1;
        }
        for (int i = 0; i < 50 && (CGMainDisplayID() != target || NSScreen.mainScreen.backingScaleFactor != 2 ||
                                   [NSScreen.screens.firstObject.deviceDescription[@"NSScreenNumber"] unsignedIntValue] != target);
             i++)
            pause_a_little();
        describe("the main display", target);
        describe("the runner's display", first);
        NSScreen *menuBar = NSScreen.screens.firstObject; // the screen with the menu bar
        fprintf(stderr, "AppKit: the menu bar's screen %u, %.0f x %.0f points at %.0fx\n",
                [menuBar.deviceDescription[@"NSScreenNumber"] unsignedIntValue], menuBar.frame.size.width,
                menuBar.frame.size.height, menuBar.backingScaleFactor);
        CGRect b = CGDisplayBounds(target);
        if (CGMainDisplayID() != target || !isRetina(target, w, h)) {
            fprintf(stderr, "the main display is not the 2x one\n");
            return 1;
        }
        NSString *line = [NSString stringWithFormat:@"%.0f %.0f 2\n", b.size.width, b.size.height];
        if (![line writeToFile:ready atomically:YES encoding:NSUTF8StringEncoding error:nil]) {
            fprintf(stderr, "could not write %s\n", argv[2]);
            return 1;
        }
        fprintf(stderr, "ready: %s", line.UTF8String);
        // kept until killed: a virtual display ends with its process
        dispatch_main();
    }
}
