// The PGS (Blu-ray) subtitle fixture of story 13.20's subtitle tests, made once and committed as
// src/test/resources/subs/arabic.sup (ffmpeg has no PGS encoder):
//
//   java libmpv/make-pgs.java ../android/core/src/main/res/font/ibm_plex_sans_arabic_regular.ttf \
//     src/test/resources/subs/arabic.sup
//
// One Arabic line, white, drawn with the Player's face (joined and right to left by Java2D), shown
// over a 1280x720 picture from 1 s to 29 s. Each pixel's coverage is one of 16 palette entries,
// white at that opacity; the picture is run-length coded as PGS's object definition segment.
import java.awt.Color;
import java.awt.Font;
import java.awt.Graphics2D;
import java.awt.RenderingHints;
import java.awt.font.TextAttribute;
import java.awt.font.TextLayout;
import java.awt.image.BufferedImage;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.text.AttributedString;

public class MakePgs {
    static final int VIDEO_W = 1280, VIDEO_H = 720, W = 640, H = 90, X = (VIDEO_W - W) / 2, Y = 600;
    static final String TEXT = "مرحبا بكم في مكتبة";

    public static void main(String[] args) throws Exception {
        Font font = Font.createFont(Font.TRUETYPE_FONT, new File(args[0])).deriveFont(48f);
        BufferedImage img = new BufferedImage(W, H, BufferedImage.TYPE_BYTE_GRAY);
        Graphics2D g = img.createGraphics();
        g.setRenderingHint(RenderingHints.KEY_TEXT_ANTIALIASING, RenderingHints.VALUE_TEXT_ANTIALIAS_ON);
        g.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON);
        g.setColor(Color.WHITE);
        AttributedString s = new AttributedString(TEXT);
        s.addAttribute(TextAttribute.FONT, font);
        s.addAttribute(TextAttribute.RUN_DIRECTION, TextAttribute.RUN_DIRECTION_RTL);
        TextLayout line = new TextLayout(s.getIterator(), g.getFontRenderContext());
        line.draw(g, (W - line.getAdvance()) / 2f, (H + line.getAscent() - line.getDescent()) / 2f);
        g.dispose();

        int[] px = new int[W * H];
        for (int y = 0; y < H; y++) for (int x = 0; x < W; x++) px[y * W + x] = Math.round((img.getRaster().getSample(x, y, 0) / 255f) * 15);
        byte[] rle = rle(px);
        if (rle.length + 4 > 0xFFFF - 7) throw new IllegalStateException("the picture needs more than one object segment");

        ByteArrayOutputStream out = new ByteArrayOutputStream();
        long show = 90_000L, clear = 29 * 90_000L;
        // the line on: composition (epoch start), window, palette, object, end
        segment(out, show, 0x16, pcs(0, 0x80, true));
        segment(out, show, 0x17, wds());
        segment(out, show, 0x14, pds());
        segment(out, show, 0x15, ods(rle));
        segment(out, show, 0x80, new byte[0]);
        // the line off: an empty composition
        segment(out, clear, 0x16, pcs(1, 0x00, false));
        segment(out, clear, 0x17, wds());
        segment(out, clear, 0x80, new byte[0]);
        try (FileOutputStream f = new FileOutputStream(args[1])) {
            out.writeTo(f);
        }
    }

    static void segment(ByteArrayOutputStream out, long pts, int type, byte[] data) {
        out.write('P');
        out.write('G');
        u32(out, pts);
        u32(out, 0);
        out.write(type);
        u16(out, data.length);
        out.writeBytes(data);
    }

    static byte[] pcs(int number, int state, boolean withObject) {
        ByteArrayOutputStream b = new ByteArrayOutputStream();
        u16(b, VIDEO_W);
        u16(b, VIDEO_H);
        b.write(0x10);
        u16(b, number);
        b.write(state);
        b.write(0);
        b.write(0);
        b.write(withObject ? 1 : 0);
        if (withObject) {
            u16(b, 0);
            b.write(0);
            b.write(0);
            u16(b, X);
            u16(b, Y);
        }
        return b.toByteArray();
    }

    static byte[] wds() {
        ByteArrayOutputStream b = new ByteArrayOutputStream();
        b.write(1);
        b.write(0);
        u16(b, X);
        u16(b, Y);
        u16(b, W);
        u16(b, H);
        return b.toByteArray();
    }

    /** 16 entries: white (Y 235, Cr and Cb 128) at opacity i/15; entry 0 transparent. */
    static byte[] pds() {
        ByteArrayOutputStream b = new ByteArrayOutputStream();
        b.write(0);
        b.write(0);
        for (int i = 0; i < 16; i++) {
            b.write(i);
            b.write(235);
            b.write(128);
            b.write(128);
            b.write(i * 17);
        }
        return b.toByteArray();
    }

    static byte[] ods(byte[] rle) {
        ByteArrayOutputStream b = new ByteArrayOutputStream();
        u16(b, 0);
        b.write(0);
        b.write(0xC0);
        int len = rle.length + 4;
        b.write((len >> 16) & 0xFF);
        b.write((len >> 8) & 0xFF);
        b.write(len & 0xFF);
        u16(b, W);
        u16(b, H);
        b.writeBytes(rle);
        return b.toByteArray();
    }

    /** PGS's run-length code, line by line, each line ended by 0x00 0x00. */
    static byte[] rle(int[] px) {
        ByteArrayOutputStream b = new ByteArrayOutputStream();
        for (int y = 0; y < H; y++) {
            int x = 0;
            while (x < W) {
                int c = px[y * W + x];
                int run = 1;
                while (x + run < W && px[y * W + x + run] == c && run < 16383) run++;
                if (c == 0) {
                    b.write(0);
                    if (run < 64) b.write(run);
                    else {
                        b.write(0x40 | (run >> 8));
                        b.write(run & 0xFF);
                    }
                } else if (run < 3) {
                    for (int i = 0; i < run; i++) b.write(c);
                } else {
                    b.write(0);
                    if (run < 64) b.write(0x80 | run);
                    else {
                        b.write(0xC0 | (run >> 8));
                        b.write(run & 0xFF);
                    }
                    b.write(c);
                }
                x += run;
            }
            b.write(0);
            b.write(0);
        }
        return b.toByteArray();
    }

    static void u16(ByteArrayOutputStream b, int v) {
        b.write((v >> 8) & 0xFF);
        b.write(v & 0xFF);
    }

    static void u32(ByteArrayOutputStream b, long v) {
        b.write((int) (v >> 24) & 0xFF);
        b.write((int) (v >> 16) & 0xFF);
        b.write((int) (v >> 8) & 0xFF);
        b.write((int) v & 0xFF);
    }
}
