// Package transpilert is the stable public runtime support surface that
// Geblang's transpiled Go output imports. It provides the support types
// (ordered dict, async task, error values, checked int arithmetic) and
// typed adapters over the engine stdlib so transpiled code reaches the
// same behavior as the interpreter without depending on internal packages.
package transpilert
