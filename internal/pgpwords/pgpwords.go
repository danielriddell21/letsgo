// Package pgpwords encodes bytes as the PGP word list (Juola & Zimmermann,
// 1995), for reading a digest aloud.
//
// Even byte positions use one list of two-syllable words and odd positions
// another of three-syllable words, so a dropped, repeated or swapped word
// breaks the rhythm and is heard rather than having to be found.
package pgpwords

import (
	"fmt"
	"strings"
)

// Encode returns one word per byte, alternating between the even and odd
// lists starting with the even one.
func Encode(data []byte) []string {
	words := make([]string, len(data))
	for i, b := range data {
		if i%2 == 0 {
			words[i] = even[b]
		} else {
			words[i] = odd[b]
		}
	}
	return words
}

var even = table(
	"aardvark absurd accrue acme adrift adult afflict ahead " +
		"aimless Algol allow alone ammo ancient apple artist " +
		"assume Athens atlas Aztec baboon backfield backward banjo " +
		"beaming bedlamp beehive beeswax befriend Belfast berserk billiard " +
		"bison blackjack blockade blowtorch bluebird bombast bookshelf brackish " +
		"breadline breakup brickyard briefcase Burbank button buzzard cement " +
		"chairlift chatter checkup chisel choking chopper Christmas clamshell " +
		"classic classroom cleanup clockwork cobra commence concert cowbell " +
		"crackdown cranky crowfoot crucial crumpled crusade cubic dashboard " +
		"deadbolt deckhand dogsled dragnet drainage dreadful drifter dropper " +
		"drumbeat drunken Dupont dwelling eating edict egghead eightball " +
		"endorse endow enlist erase escape exceed eyeglass eyetooth " +
		"facial fallout flagpole flatfoot flytrap fracture framework freedom " +
		"frighten gazelle Geiger glitter glucose goggles goldfish gremlin " +
		"guidance hamlet highchair hockey indoors indulge inverse involve " +
		"island jawbone keyboard kickoff kiwi klaxon locale lockup " +
		"merit minnow miser Mohawk mural music necklace Neptune " +
		"newborn nightbird Oakland obtuse offload optic orca payday " +
		"peachy pheasant physique playhouse Pluto preclude prefer preshrunk " +
		"printer prowler pupil puppy python quadrant quiver quota " +
		"ragtime ratchet rebirth reform regain reindeer rematch repay " +
		"retouch revenge reward rhythm ribcage ringbolt robust rocker " +
		"ruffled sailboat sawdust scallion scenic scorecard Scotland seabird " +
		"select sentence shadow shamrock showgirl skullcap skydive slingshot " +
		"slowdown snapline snapshot snowcap snowslide solo southward soybean " +
		"spaniel spearhead spellbind spheroid spigot spindle spyglass stagehand " +
		"stagnate stairway standard stapler steamship sterling stockman stopwatch " +
		"stormy sugar surmount suspense sweatband swelter tactics talon " +
		"tapeworm tempest tiger tissue tonic topmost tracker transit " +
		"trauma treadmill Trojan trouble tumor tunnel tycoon uncut " +
		"unearth unwind uproot upset upshot vapor village virus " +
		"Vulcan waffle wallet watchword wayside willow woodlark Zulu",
)

var odd = table(
	"adroitness adviser aftermath aggregate alkali almighty amulet amusement " +
		"antenna applicant Apollo armistice article asteroid Atlantic atmosphere " +
		"autopsy Babylon backwater barbecue belowground bifocals bodyguard bookseller " +
		"borderline bottomless Bradbury bravado Brazilian breakaway Burlington businessman " +
		"butterfat Camelot candidate cannonball Capricorn caravan caretaker celebrate " +
		"cellulose certify chambermaid Cherokee Chicago clergyman coherence combustion " +
		"commando company component concurrent confidence conformist congregate consensus " +
		"consulting corporate corrosion councilman crossover crucifix cumbersome customer " +
		"Dakota decadence December decimal designing detector detergent determine " +
		"dictator dinosaur direction disable disbelief disruptive distortion document " +
		"embezzle enchanting enrollment enterprise equation equipment escapade Eskimo " +
		"everyday examine existence exodus fascinate filament finicky forever " +
		"fortitude frequency gadgetry Galveston getaway glossary gossamer graduate " +
		"gravity guitarist hamburger Hamilton handiwork hazardous headwaters hemisphere " +
		"hesitate hideaway holiness hurricane hydraulic impartial impetus inception " +
		"indigo inertia infancy inferno informant insincere insurgent integrate " +
		"intention inventive Istanbul Jamaica Jupiter leprosy letterhead liberty " +
		"maritime matchmaker maverick Medusa megaton microscope microwave midsummer " +
		"millionaire miracle misnomer molasses molecule Montana monument mosquito " +
		"narrative nebula newsletter Norwegian October Ohio onlooker opulent " +
		"Orlando outfielder Pacific pandemic Pandora paperweight paragon paragraph " +
		"paramount passenger pedigree Pegasus penetrate perceptive performance pharmacy " +
		"phonetic photograph pioneer pocketful politeness positive potato processor " +
		"provincial proximate puberty publisher pyramid quantity racketeer rebellion " +
		"recipe recover repellent replica reproduce resistor responsive retraction " +
		"retrieval retrospect revenue revival revolver sandalwood sardonic Saturday " +
		"savagery scavenger sensation sociable souvenir specialist speculate stethoscope " +
		"stupendous supportive surrender suspicious sympathy tambourine telephone therapist " +
		"tobacco tolerance tomorrow torpedo tradition travesty trombonist truncated " +
		"typewriter ultimate undaunted underfoot unicorn unify universe unravel " +
		"upcoming vacancy vagabond vertigo Virginia visitor vocalist voyager " +
		"warranty Waterloo whimsical Wichita Wilmington Wyoming yesteryear Yucatan",
)

// table splits one of the two word lists, written as space-separated words
// to keep the source compact, into its 256 entries.
func table(words string) [256]string {
	var t [256]string
	if copy(t[:], strings.Fields(words)) != len(t) {
		panic("pgpwords: a word list must hold exactly 256 words")
	}
	return t
}

// wordsPerRow is how many words Rows puts on a line: four is a phrase a
// person can read out without losing their place.
const wordsPerRow = 4

// Rows lays words out in numbered rows of four, each numbered by its first
// word (1, 5, 9, ...), so a listener can be told "line 5" rather than to count.
func Rows(words []string) string {
	width := 0
	for _, w := range words {
		width = max(width, len(w))
	}

	var b strings.Builder
	for start := 0; start < len(words); start += wordsPerRow {
		fmt.Fprintf(&b, "%3d", start+1)
		for _, w := range words[start:min(start+wordsPerRow, len(words))] {
			fmt.Fprintf(&b, "  %-*s", width, w)
		}
		b.WriteString("\n")
	}
	return b.String()
}
