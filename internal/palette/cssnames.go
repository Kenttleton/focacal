package palette

// nearestCSSName finds the nearest CSS3 named color by squared RGB distance.
func NearestCSSName(hexColor string) string {
	r, g, b, err := Parse(hexColor)
	if err != nil {
		return ""
	}
	best := ""
	bestDist := -1
	for name, h := range cssColors {
		cr, cg, cb, _ := Parse(h)
		dr := int(r) - int(cr)
		dg := int(g) - int(cg)
		db := int(b) - int(cb)
		dist := dr*dr + dg*dg + db*db
		if bestDist == -1 || dist < bestDist {
			bestDist = dist
			best = name
		}
	}
	return best
}

var cssColors = map[string]string{
	"red":    "#FF0000",
	"green":  "#008000",
	"blue":   "#0000FF",
	"black":  "#000000",
	"white":  "#FFFFFF",
	"yellow": "#FFFF00",
	"orange": "#FFA500",
	"purple": "#800080",
	"gray":   "#808080",
	"pink":   "#FFC0CB",
	"brown":  "#A52A2A",
	"cyan":   "#00FFFF",
	"lime":   "#00FF00",
	"navy":   "#000080",
	"teal":   "#008080",
	"silver": "#C0C0C0",
	"gold":   "#FFD700",
	"indigo": "#4B0082",
	"violet": "#EE82EE",
	"coral":  "#FF7F50",
}
