package com.dwgebler.geblang.highlighting

/**
 * Finds `${...}` interpolation spans inside the raw text of a Geblang STRING
 * token leaf. Pure/stateless so it is fully unit-testable without any PSI or
 * platform dependencies.
 */
object GeblangInterpolation {
    /**
     * Returns the sub-ranges (relative to the start of [stringText], i.e. index 0
     * is the leaf's first character) of each `${...}` span, INCLUDING the
     * leading `${` and the trailing `}`. Returns an empty list for raw
     * (single-quoted) strings, for strings with no interpolation, or for any
     * unterminated `${` (no matching `}` found before the string leaf's text
     * ends).
     */
    fun ranges(stringText: String): List<IntRange> {
        // starts-with-quote check: only double-quoted / triple-double-quoted
        // strings interpolate. A leaf starting with ' is raw -> empty.
        if (stringText.isEmpty() || stringText[0] != '"') return emptyList()

        val result = mutableListOf<IntRange>()
        var i = 0
        val n = stringText.length
        while (i < n) {
            if (stringText[i] == '$' && i + 1 < n && stringText[i + 1] == '{') {
                val start = i
                var depth = 1
                var j = i + 2
                while (j < n && depth > 0) {
                    when (stringText[j]) {
                        '{' -> depth++
                        '}' -> depth--
                    }
                    j++
                }
                if (depth == 0) {
                    // j is one past the matched closing '}'
                    result.add(start until j)
                    i = j
                } else {
                    // unterminated - skip, no range emitted; stop scanning
                    // (nothing valid can follow inside an unterminated span)
                    break
                }
            } else {
                i++
            }
        }
        return result
    }
}
