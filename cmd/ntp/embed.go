// Embed dashboard.html як частину бінарника.
package main

import _ "embed"

//go:embed dashboard.html
var dashboardBytes []byte
