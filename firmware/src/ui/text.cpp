#include "ui/text.hpp"

#include <cstring>

namespace managents::ui {

const lgfx::IFont* const kBoldFonts[] = {
    &fonts::FreeSansBold24pt7b,
    &fonts::FreeSansBold18pt7b,
    &fonts::FreeSansBold12pt7b,
    &fonts::FreeSansBold9pt7b,
};
const std::size_t kBoldFontCount = sizeof kBoldFonts / sizeof kBoldFonts[0];

const lgfx::IFont* selectLargestFitting(lgfx::LovyanGFX& canvas, const lgfx::IFont* const* fonts, std::size_t count,
                                        const char* text, std::int32_t maxWidth, std::int32_t maxHeight) {
    for (std::size_t i = 0; i < count; ++i) {
        canvas.setFont(fonts[i]);
        if (canvas.textWidth(text) <= maxWidth && canvas.fontHeight() <= maxHeight) {
            return fonts[i];
        }
    }
    canvas.setFont(fonts[count - 1]);
    return fonts[count - 1];
}

namespace {

// Names stay modest so long folder names fit whole; the status label is the big text.
const lgfx::IFont* const kNameFonts[] = {
    &fonts::FreeSansBold12pt7b,
    &fonts::FreeSansBold9pt7b,
    &fonts::FreeSans9pt7b,
};

bool isBreakAfter(char c) {
    return c == '-' || c == '_' || c == '/' || c == '.' || c == ' ';
}
bool isContinuation(char c) {
    return (static_cast<unsigned char>(c) & 0xC0) == 0x80;
}

/// Length of the longest prefix of `text` that fits in `maxWidth`, moved back
/// to just after a separator when there is one in the second half.
std::size_t wrapPoint(lgfx::LovyanGFX& canvas, const char* text, std::int32_t maxWidth) {
    char buffer[NameLayout::kLineCapacity];
    const std::size_t length = std::strlen(text);
    std::size_t fits = 0;
    for (std::size_t end = 1; end <= length && end < sizeof buffer; ++end) {
        if (end < length && isContinuation(text[end])) {
            continue;  // never split a UTF-8 character
        }
        std::memcpy(buffer, text, end);
        buffer[end] = '\0';
        if (canvas.textWidth(buffer) > maxWidth) {
            break;
        }
        fits = end;
    }
    for (std::size_t i = fits; i > fits / 2; --i) {
        if (isBreakAfter(text[i - 1])) {
            return i;
        }
    }
    return fits;
}

void copyLine(const char* text, std::size_t length, char* out) {
    length = length < NameLayout::kLineCapacity - 1 ? length : NameLayout::kLineCapacity - 1;
    std::memcpy(out, text, length);
    out[length] = '\0';
}

}  // namespace

NameLayout layoutName(lgfx::LovyanGFX& canvas, const char* text, std::int32_t maxWidth, std::int32_t maxHeight) {
    NameLayout layout;
    const std::size_t fontCount = sizeof kNameFonts / sizeof kNameFonts[0];
    const lgfx::IFont* smallest = kNameFonts[fontCount - 1];

    for (const lgfx::IFont* font : kNameFonts) {
        canvas.setFont(font);
        if (canvas.fontHeight() <= maxHeight && canvas.textWidth(text) <= maxWidth) {
            layout.font = font;
            copyLine(text, std::strlen(text), layout.lines[0]);
            layout.lineCount = 1;
            return layout;
        }
    }

    canvas.setFont(smallest);
    layout.font = smallest;
    if (2 * canvas.fontHeight() > maxHeight) {
        ellipsize(canvas, text, maxWidth, layout.lines[0], NameLayout::kLineCapacity);
        layout.lineCount = 1;
        return layout;
    }
    const std::size_t split = wrapPoint(canvas, text, maxWidth);
    copyLine(text, split, layout.lines[0]);
    ellipsize(canvas, text + split, maxWidth, layout.lines[1], NameLayout::kLineCapacity);
    layout.lineCount = 2;
    return layout;
}

void ellipsize(lgfx::LovyanGFX& canvas, const char* text, std::int32_t maxWidth, char* out, std::size_t capacity) {
    static constexpr char kEllipsis[] = "...";
    std::strncpy(out, text, capacity - 1);
    out[capacity - 1] = '\0';
    if (canvas.textWidth(out) <= maxWidth) {
        return;
    }

    std::size_t length = std::strlen(out);
    const std::size_t reserve = sizeof kEllipsis - 1;
    if (length + reserve >= capacity) {
        length = capacity - 1 - reserve;
    }
    while (length > 0) {
        do {
            --length;  // step back over a whole UTF-8 sequence
        } while (length > 0 && (static_cast<unsigned char>(out[length]) & 0xC0) == 0x80);
        std::memcpy(out + length, kEllipsis, reserve + 1);
        if (canvas.textWidth(out) <= maxWidth) {
            return;
        }
    }
}

}  // namespace managents::ui
