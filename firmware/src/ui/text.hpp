#pragma once

#include <cstddef>
#include <cstdint>

#include <LovyanGFX.hpp>

namespace managents::ui {

/// Bold sans fonts from largest to smallest.
extern const lgfx::IFont* const kBoldFonts[];
extern const std::size_t kBoldFontCount;

/// Selects the largest font from `fonts` (ordered largest first) in which
/// `text` fits within `maxWidth` x `maxHeight`; falls back to the smallest.
/// The chosen font is left selected on `canvas`.
const lgfx::IFont* selectLargestFitting(lgfx::LovyanGFX& canvas, const lgfx::IFont* const* fonts, std::size_t count,
                                        const char* text, std::int32_t maxWidth, std::int32_t maxHeight);

/// A card name fitted into a box: the largest readable font, on one line if
/// possible, otherwise wrapped onto two (preferably after - _ / . or a space),
/// and shortened with "..." only as a last resort.
struct NameLayout {
    static constexpr std::size_t kLineCapacity = 96;
    const lgfx::IFont* font = nullptr;
    char lines[2][kLineCapacity] = {};
    std::size_t lineCount = 0;
};

NameLayout layoutName(lgfx::LovyanGFX& canvas, const char* text, std::int32_t maxWidth, std::int32_t maxHeight);

/// Copies `text` into `out`, cut at a UTF-8 boundary and suffixed with "..."
/// if it would be wider than `maxWidth` in the canvas' current font.
void ellipsize(lgfx::LovyanGFX& canvas, const char* text, std::int32_t maxWidth, char* out, std::size_t capacity);

}  // namespace managents::ui
