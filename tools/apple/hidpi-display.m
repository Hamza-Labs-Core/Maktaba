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
// keeps it while it runs: the virtual display goes when the process ends. Exits 1, with the
// modes it saw, when it cannot.
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

/// A tenth of a second, the main queue served meanwhile (the virtual display's callbacks).
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

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        unsigned int w = 0, h = 0;
        if (argc != 3 || sscanf(argv[1], "%ux%u", &w, &h) != 2 || w == 0 || h == 0) {
            fprintf(stderr, "usage: hidpi-display WIDTHxHEIGHT READY_FILE\n");
            return 2;
        }
        NSString *ready = [NSString stringWithUTF8String:argv[2]];
        CGDirectDisplayID first = CGMainDisplayID();
        fprintf(stderr, "the main display (%u) is %zu x %zu pixels; its modes:\n", first, CGDisplayPixelsWide(first),
                CGDisplayPixelsHigh(first));
        printModes(first);

        CGDirectDisplayID target = first;
        CGDisplayModeRef mode = retinaMode(first, w, h);
        if (!mode) {
            // none of its own: a virtual display, its one mode 2w x 2h pixels, offered at 2x
            CGVirtualDisplayDescriptor *desc = [[NSClassFromString(@"CGVirtualDisplayDescriptor") alloc] init];
            if (!desc) {
                fprintf(stderr, "this macOS has no CGVirtualDisplay\n");
                return 1;
            }
            [desc setDispatchQueue:dispatch_get_main_queue()];
            desc.name = @"Maktaba shots";
            desc.maxPixelsWide = 2 * w;
            desc.maxPixelsHigh = 2 * h;
            // a 16-inch screen's size: 2 * w pixels on it are a Retina density
            desc.sizeInMillimeters = CGSizeMake(345, 345.0 * h / w);
            desc.productID = 0x6d6b;
            desc.vendorID = 0x6d6b;
            desc.serialNum = 1;
            desc.terminationHandler = ^(id a, id b) { fprintf(stderr, "the virtual display ended\n"); };
            CGVirtualDisplay *display = [[NSClassFromString(@"CGVirtualDisplay") alloc] initWithDescriptor:desc];
            kept = display;
            if (!display || display.displayID == 0) {
                fprintf(stderr, "the virtual display was not made\n");
                return 1;
            }
            CGVirtualDisplaySettings *settings = [[NSClassFromString(@"CGVirtualDisplaySettings") alloc] init];
            settings.hiDPI = 1;
            settings.modes = @[
                [[NSClassFromString(@"CGVirtualDisplayMode") alloc] initWithWidth:2 * w height:2 * h refreshRate:60],
                [[NSClassFromString(@"CGVirtualDisplayMode") alloc] initWithWidth:w height:h refreshRate:60],
            ];
            if (![display applySettings:settings]) {
                fprintf(stderr, "the virtual display took no settings\n");
                return 1;
            }
            target = display.displayID;
            for (int i = 0; i < 100 && !online(target); i++)
                pause_a_little();
            // its modes come a moment after it is online (none at first on GitHub's runner)
            for (int i = 0; i < 150 && !(mode = retinaMode(target, w, h)); i++) {
                // its settings again now and then, should the first have come too early
                if (i % 50 == 49) fprintf(stderr, "settings again: %d\n", [display applySettings:settings]);
                pause_a_little();
            }
            fprintf(stderr, "the virtual display (%u), online: %d; its modes:\n", target, online(target));
            printModes(target);
            if (!mode) {
                fprintf(stderr, "the virtual display has no 2x mode of %u x %u points\n", w, h);
                return 1;
            }
        }

        CGDisplayConfigRef config;
        CGBeginDisplayConfiguration(&config);
        CGConfigureDisplayWithDisplayMode(config, target, mode, NULL);
        if (target != first) {
            // the main display is the one at the origin: the virtual one, the runner's beside it
            CGConfigureDisplayOrigin(config, target, 0, 0);
            CGConfigureDisplayOrigin(config, first, (int32_t)CGDisplayModeGetWidth(mode), 0);
        }
        CGError err = CGCompleteDisplayConfiguration(config, kCGConfigureForSession);
        CGDisplayModeRelease(mode);
        if (err != kCGErrorSuccess) {
            fprintf(stderr, "the display configuration failed: %d\n", err);
            return 1;
        }
        // the screens as AppKit sees them, once the change is through
        for (int i = 0; i < 50 && CGMainDisplayID() != target; i++)
            pause_a_little();
        CGDisplayModeRef now = CGDisplayCopyDisplayMode(target);
        size_t pw = CGDisplayModeGetPixelWidth(now), pt = CGDisplayModeGetWidth(now);
        size_t ph = CGDisplayModeGetPixelHeight(now), pth = CGDisplayModeGetHeight(now);
        CGDisplayModeRelease(now);
        fprintf(stderr, "main display %u (target %u): %zu x %zu points, %zu x %zu pixels\n", CGMainDisplayID(), target, pt,
                pth, pw, ph);
        if (CGMainDisplayID() != target || pw != 2 * pt) {
            fprintf(stderr, "the main display is not the 2x one\n");
            return 1;
        }
        NSString *line = [NSString stringWithFormat:@"%zu %zu %zu\n", pt, pth, pw / pt];
        if (![line writeToFile:ready atomically:YES encoding:NSUTF8StringEncoding error:nil]) {
            fprintf(stderr, "could not write %s\n", argv[2]);
            return 1;
        }
        fprintf(stderr, "ready: %s", line.UTF8String);
        // kept until killed: a virtual display ends with its process
        dispatch_main();
    }
}
