// SPDX-License-Identifier: Apache-2.0
// Package reportassets embeds the prebuilt report frontend.
package reportassets

import _ "embed"

// Script is the compiled JavaScript for the report frontend.
//
//go:embed report.js
var Script string

// CSS contains the compiled styles for the report frontend.
//
//go:embed report.css
var CSS string
