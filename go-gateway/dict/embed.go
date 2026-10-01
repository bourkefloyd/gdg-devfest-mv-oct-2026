// Package dict embeds the normalized ENABLE word list.
package dict

import _ "embed"

//go:embed enable1.txt
var ENABLE string
