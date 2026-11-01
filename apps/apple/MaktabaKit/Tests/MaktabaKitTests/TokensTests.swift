import Testing
@testable import MaktabaKit

/// The generated Tokens.swift (Story 48.2) carries Ink & Brass from web/design-system/tokens/.
@Suite struct DesignTokens {
    @Test func darkIsInkAndBrass() {
        #expect(MaktabaTokens.Colors.bg.dark == 0x0B11_1CFF)
        #expect(MaktabaTokens.Colors.accent.dark == 0xE3B2_52FF)
        #expect(MaktabaTokens.Colors.bg.light == 0xFFFF_FFFF)
        #expect(MaktabaTokens.Colors.accentText.light == 0x8A62_12FF)
    }

    @Test func keepsTranslucencyAndScales() {
        #expect(MaktabaTokens.Colors.scrim.dark & 0xFF == 0x8F)
        #expect(MaktabaTokens.Spacing.s4 == 16)
        #expect(MaktabaTokens.Radius.lg == 12)
        #expect(MaktabaTokens.TypeSize.base == 16)
        #expect(MaktabaTokens.Fonts.heading == "Amiri")
        _ = MaktabaTokens.Colors.bg.color
    }
}
