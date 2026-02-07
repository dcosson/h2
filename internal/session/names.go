package session

import (
	"math/rand/v2"
)

// adjectives for name generation.
var adjectives = []string{
	"amber", "azure", "bold", "brave", "bright",
	"calm", "clear", "cool", "coral", "crisp",
	"dawn", "deep", "deft", "dry", "dusk",
	"fair", "fast", "firm", "fond", "free",
	"glad", "gold", "good", "gray", "green",
	"hale", "high", "keen", "kind", "lark",
	"lean", "lime", "live", "long", "loud",
	"mild", "mint", "neat", "next", "nice",
	"odd", "opal", "open", "pale", "peak",
	"pine", "pure", "quick", "rare", "red",
	"rich", "ripe", "rose", "ruby", "sage",
	"salt", "slim", "soft", "sure", "tall",
	"teal", "tidy", "trim", "true", "warm",
	"west", "wide", "wild", "wise", "zinc",
}

// nouns for name generation.
var nouns = []string{
	"arch", "barn", "bay", "bell", "birch",
	"bloom", "boat", "bolt", "bone", "book",
	"brook", "cape", "cave", "clay", "cliff",
	"cloud", "coin", "cove", "crow", "dale",
	"deer", "dove", "drum", "dune", "elm",
	"fern", "finch", "fish", "flint", "fog",
	"ford", "fox", "frost", "gate", "gem",
	"glen", "glow", "grove", "gull", "hare",
	"hawk", "heath", "heron", "hill", "hive",
	"isle", "jade", "jay", "keel", "knoll",
	"lake", "lark", "leaf", "loch", "lynx",
	"maple", "marsh", "mill", "mist", "moon",
	"moss", "moth", "oak", "owl", "path",
	"peak", "pine", "plum", "pond", "quail",
	"rain", "reed", "reef", "ridge", "river",
	"rock", "root", "sand", "seal", "shore",
	"snow", "spark", "star", "stone", "storm",
	"swift", "thorn", "tide", "trail", "vale",
	"vine", "wren", "wolf", "wood", "yarn",
}

// GenerateName produces a random adjective-noun name like "calm-brook".
func GenerateName() string {
	adj := adjectives[rand.IntN(len(adjectives))]
	noun := nouns[rand.IntN(len(nouns))]
	return adj + "-" + noun
}
