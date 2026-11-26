// Package gamedata holds the read-only game catalog (items, skills, jobs)
// loaded into memory at startup.
package gamedata

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Langs supported by the UI and catalog text.
var Langs = []string{"pt", "en", "es"}

// Text is localized text keyed by language ("pt", "en", "es").
type Text map[string]string

// Get returns the text in lang, falling back to pt, then any language.
func (t Text) Get(lang string) string {
	if s := t[lang]; s != "" {
		return s
	}
	if s := t["pt"]; s != "" {
		return s
	}
	for _, s := range t {
		return s
	}
	return ""
}

// Lines is localized multi-line text (client descriptions with ^RRGGBB codes).
type Lines map[string][]string

// Get returns the lines in lang with the same fallback as Text.
func (l Lines) Get(lang string) []string {
	if s := l[lang]; len(s) > 0 {
		return s
	}
	if s := l["pt"]; len(s) > 0 {
		return s
	}
	for _, s := range l {
		return s
	}
	return nil
}

// Item is one item: names/descriptions from the client, mechanics from rAthena.
type Item struct {
	ID       int    `json:"id"`
	Name     Text   `json:"name"`
	Desc     Lines  `json:"desc"`
	Slots    int    `json:"slots"`
	ClassNum int    `json:"classNum"`
	Resource string `json:"resource"`

	// From rAthena (empty when the item is not equipment or a card).
	Type        string   `json:"type,omitempty"`    // Weapon, Armor, Card, ShadowGear, ...
	SubType     string   `json:"subType,omitempty"` // weapon type: Dagger, 1hSword, ...
	Locations   []string `json:"loc,omitempty"`     // Head_Top, Armor, Right_Hand, ...
	Attack      int      `json:"atk,omitempty"`
	MagicAttack int      `json:"matk,omitempty"`
	Defense     int      `json:"def,omitempty"`
	WeaponLv    int      `json:"wlv,omitempty"`
	ArmorLv     int      `json:"alv,omitempty"`
	EquipLvMin  int      `json:"elv,omitempty"`
	EquipLvMax  int      `json:"elvMax,omitempty"`
	Refineable  bool     `json:"refine,omitempty"`
	Gradable    bool     `json:"grade,omitempty"`     // takes an enchant grade (D..A)
	MVPCard     bool     `json:"mvpCard,omitempty"`   // a card only MVPs drop
	DescEquip   bool     `json:"descEquip,omitempty"` // type/slot/ATK/DEF read from the client description (LATAM-only item)
	Script      string   `json:"script,omitempty"`
	// Preview: not in the LATAM client yet.
	Preview bool `json:"preview,omitempty"`
	// For items without an rAthena script: bonuses read from the client
	// description (clientimport.DescScript) and the lines it skipped.
	DescScript  string `json:"dscript,omitempty"`
	DescSkipped int    `json:"dskip,omitempty"`
	// EventScript: the description's "[Durante o Evento]" bonuses, which
	// count only when the build turns them on.
	EventScript string `json:"evscript,omitempty"`
	// latam-ro-calc's script (item.json, MIT): bonus key -> lines in its own
	// notation. LatamName is the item's English name there, for EQUIP[...]
	// conditions.
	// LatamKeys are the bonuses (Bonus names / Extra keys) where the LATAM
	// script wins over the rAthena one (calc.DecideLatamKeys); Latam is
	// kept only for those items.
	Latam     map[string][]string `json:"lt,omitempty"`
	LatamName string              `json:"ltn,omitempty"`
	LatamKeys []string            `json:"ltk,omitempty"`
	// LatamProcs: the item's triggered bonuses in latam-ro-calc
	// ("chance__<key>" lines), for items whose rAthena script has no
	// autobonus.
	LatamProcs map[string][]string `json:"ltp,omitempty"`
	// LatamUse: a consumable's bonuses while its status lasts
	// (latam-ro-calc item.json, consumables only).
	LatamUse map[string][]string `json:"ltu,omitempty"`
	// Who can equip it (rAthena Jobs/Classes); empty means everyone.
	EquipJobs    []string `json:"jobs,omitempty"`
	EquipClasses []string `json:"classes,omitempty"`
	Weight       int      `json:"wt,omitempty"` // tenths (rAthena)
}

// HasLocation reports whether the item can go in a slot location.
func (it *Item) HasLocation(loc string) bool {
	if it == nil {
		return false
	}
	for _, l := range it.Locations {
		if l == loc {
			return true
		}
	}
	return false
}

// Req is a skill prerequisite.
type Req struct {
	Skill int `json:"skill"`
	Level int `json:"level"`
}

// Skill is one skill from the client's skillinfolist.
type Skill struct {
	ID    int    `json:"id"`
	Code  string `json:"code"` // e.g. KN_BOWLINGBASH
	Name  Text   `json:"name"`
	Desc  Lines  `json:"desc"`
	MaxLv int    `json:"maxLv"`
	SP    []int  `json:"sp,omitempty"`
	Range []int  `json:"range,omitempty"`
	// Needs are the default prerequisites; NeedsByJob overrides them for
	// specific jobs (the client's NeedSkillList).
	Needs      []Req         `json:"needs,omitempty"`
	NeedsByJob map[int][]Req `json:"needsByJob,omitempty"`
	// Type from the client: "Quest" (learned by quest) or "Soul" (granted by
	// Soul Link). Such skills cost no skill points.
	Type string `json:"type,omitempty"`
	// Effects are the status bonuses read from the client description's
	// level table (clientimport.SkillDescEffects); nil when it has none the
	// simulator understands.
	Effects *SkillEffects `json:"effects,omitempty"`
	// Weapons are the weapon types the skill requires (rAthena skill_db
	// Requires: Weapon); empty = any.
	Weapons []string `json:"weapons,omitempty"`
	// Passive: rAthena gives it no TargetType (never cast).
	Passive bool `json:"passive,omitempty"`
}

// SkillEffects: what a buff or passive gives at each level, from the
// client's own description (LATAM values).
type SkillEffects struct {
	Kind    string         `json:"kind"`              // "passive" (counts at the learned level) or "buff" (turned on)
	Weapons []string       `json:"weapons,omitempty"` // only with these weapon types (rAthena SubType); empty = any
	Group   string         `json:"group,omitempty"`   // buffs that cannot be on together share it
	Levels  map[int]string `json:"levels"`            // level -> rAthena script
	Unread  []string       `json:"unread,omitempty"`  // table columns not turned into bonuses
}

// Free reports whether the skill costs no skill points.
func (s *Skill) Free() bool { return s.Type == "Quest" || s.Type == "Soul" }

// NeedsFor returns the prerequisites that apply to a job chain (closest job first).
func (s *Skill) NeedsFor(chain []int) []Req {
	for i := len(chain) - 1; i >= 0; i-- {
		if r, ok := s.NeedsByJob[chain[i]]; ok {
			return r
		}
	}
	return s.Needs
}

// Job is a playable class with its own skill tab.
type Job struct {
	ID     int    `json:"id"`
	Code   string `json:"code"` // e.g. JT_KNIGHT
	Name   Text   `json:"name"`
	Parent int    `json:"parent"` // -1 for none
	// Tree maps the client's skill-window grid position (row*7+col) to a skill ID.
	Tree map[int]int `json:"tree"`

	// From rAthena.
	MaxBaseLv int            `json:"maxBaseLv,omitempty"`
	MaxJobLv  int            `json:"maxJobLv,omitempty"`
	MaxWeight int            `json:"maxWeight,omitempty"`
	BaseHP    []int          `json:"baseHp,omitempty"` // index: base level - 1
	BaseSP    []int          `json:"baseSp,omitempty"`
	BaseAP    []int          `json:"baseAp,omitempty"`
	JobBonus  [][12]int      `json:"jobBonus,omitempty"` // index: job level; cumulative STR..LUK, POW..CRT
	BaseASPD  map[string]int `json:"baseAspd,omitempty"` // weapon type -> base delay
	Upper     bool           `json:"upper,omitempty"`    // MaxHP/SP x1.25 (transcendent, primary 4th)
	Fourth    bool           `json:"fourth,omitempty"`   // has trait stats
	Baby      bool           `json:"baby,omitempty"`
	// Equipment restrictions: rAthena job group (Knight, BardDancer, ...) and
	// class tier (Normal, Upper, Baby, Third, Third_Upper, Third_Baby, Fourth).
	EquipGroup string `json:"equipGroup,omitempty"`
	EquipTier  string `json:"equipTier,omitempty"`
}

// tierSets expands rAthena's grouped class flags (itemdb.hpp e_item_job).
var tierSets = map[string][]string{
	"All_Upper": {"Upper", "Third_Upper", "Fourth"},
	"All_Baby":  {"Baby", "Third_Baby"},
	"All_Third": {"Third", "Third_Upper", "Third_Baby"},
}

// CanEquip mirrors rAthena's renewal pc_isItemClass + job check. Jobs without
// imported restrictions (no EquipGroup) can equip anything.
func (j *Job) CanEquip(it *Item) bool {
	if j == nil || it == nil {
		return true
	}
	// Weapons without a job list (LATAM-only, read from the description):
	// the class must have an ASPD for that weapon type.
	if it.Type == "Weapon" && len(it.EquipJobs) == 0 && it.SubType != "" && len(j.BaseASPD) > 0 {
		if _, ok := j.BaseASPD[it.SubType]; !ok {
			return false
		}
	}
	if j.EquipGroup == "" {
		return true
	}
	if j.Code == "JT_SPIRIT_HANDLER" && slices.Contains(it.EquipJobs, "Spirit_Handler") {
		return true
	}
	if len(it.EquipJobs) > 0 && !slices.Contains(it.EquipJobs, "All") && !slices.Contains(it.EquipJobs, j.EquipGroup) {
		return false
	}
	if len(it.EquipClasses) == 0 || slices.Contains(it.EquipClasses, "All") {
		return true
	}
	for _, c := range it.EquipClasses {
		if c == j.EquipTier || slices.Contains(tierSets[c], j.EquipTier) {
			return true
		}
	}
	return false
}

// HasStats reports whether rAthena job tables were imported for this job.
func (j *Job) HasStats() bool { return j.MaxBaseLv > 0 && len(j.BaseHP) > 0 }

// TreeColumns is the width of the client's skill window grid.
const TreeColumns = 7

// Catalog is all game data.
type Catalog struct {
	Generated time.Time     `json:"generated"`
	Source    string        `json:"source"`
	Items     map[int]*Item `json:"items"`
	// Total status/trait points at each base level (index: level - 1), from rAthena.
	StatPoints  []int `json:"statPoints,omitempty"`
	TraitPoints []int `json:"traitPoints,omitempty"`
	// Refine[group][equipLevel][refine] = cumulative bonus in 1/100 (rAthena refine.yml).
	Refine map[string]map[int][]int `json:"refine,omitempty"`
	// GradeBonus[Weapon|Armor][level][grade] = % added to the refine bonus.
	GradeBonus map[string]map[int][5]int `json:"gradeBonus,omitempty"`
	// RandomOpts are the random options an item can roll, by option ID.
	RandomOpts map[int]*RandomOpt `json:"randomOpts,omitempty"`
	// ItemOpts: which random options each item can get (items missing here
	// have no known random options).
	ItemOpts map[int]*ItemOpts `json:"itemOpts,omitempty"`
	// Monsters and damage tables (rAthena renewal).
	Monsters map[int]*Monster `json:"monsters,omitempty"`
	// AttrFix[defElementLevel][atkElement][defElement] = damage %.
	AttrFix map[int]map[string]map[string]int `json:"attrFix,omitempty"`
	// SizeFix[weaponType][Small|Medium|Large] = damage %; missing = 100.
	SizeFix map[string]map[string]int `json:"sizeFix,omitempty"`
	Skills  map[int]*Skill            `json:"skills"`
	Jobs    map[int]*Job              `json:"jobs"`
	// Item combos (rAthena item_combos.yml): the script applies when every
	// listed item (an ID twice = two copies) is equipped, cards included.
	Combos []Combo `json:"combos,omitempty"`
	// Enchants[item][card index] = enchant item IDs the item accepts there
	// (client enchant list). Items missing here have no known enchant data.
	Enchants map[int]map[int][]int `json:"enchants,omitempty"`
	// Pets by egg item ID (rAthena pet_db.yml): the monster and the bonus
	// script, whose tiers depend on intimacy (0-1000).
	Pets map[int]*Pet `json:"pets,omitempty"`
	// MonsterStubs are monsters rAthena knows only by ID (ID -> AegisName);
	// their stats are fetched on demand (Divine Pride) and kept with AddMonster.
	MonsterStubs map[int]string `json:"monsterStubs,omitempty"`

	extraMobs sync.Map // int -> *Monster, loaded at run time

	comboOnce sync.Once
	comboIdx  map[int][]int // item ID -> indexes into Combos
}

// Combo is an item set and the script it unlocks.
type Combo struct {
	Items  []int  `json:"i"`
	Script string `json:"s"`
}

// CombosWith returns the indexes of the combos that include item id.
func (c *Catalog) CombosWith(id int) []int {
	c.comboOnce.Do(func() {
		c.comboIdx = map[int][]int{}
		for i, cb := range c.Combos {
			seen := map[int]bool{}
			for _, it := range cb.Items {
				if !seen[it] {
					c.comboIdx[it] = append(c.comboIdx[it], i)
					seen[it] = true
				}
			}
		}
	})
	return c.comboIdx[id]
}

// SkillGroup is one class step's skill tabs and its skill point budget.
type SkillGroup struct {
	Jobs   []int `json:"jobs"`
	Budget int   `json:"budget"` // -1 = unknown (no job tables imported)
}

// SkillBudgets splits a job's chain into class steps with their skill point
// budgets (renewal): max job level - 1 for completed steps, current job
// level - 1 for the last one (jobLv <= 0 means max). A class and its
// transcendent version (KNIGHT, KNIGHT_H) share one step. Leftover points
// carry forward, so callers check cumulative sums per step.
func (c *Catalog) SkillBudgets(jobID, jobLv int) []SkillGroup {
	chain := c.Chain(jobID)
	var groups []SkillGroup
	for i, id := range chain {
		j := c.Jobs[id]
		if i > 0 && len(groups) > 0 {
			prev := c.Jobs[chain[i-1]]
			if j.Code == prev.Code+"_H" { // transcendent version: same step
				groups[len(groups)-1].Jobs = append(groups[len(groups)-1].Jobs, id)
				continue
			}
		}
		groups = append(groups, SkillGroup{Jobs: []int{id}})
	}
	for gi := range groups {
		last := c.Jobs[groups[gi].Jobs[len(groups[gi].Jobs)-1]]
		switch {
		case last.MaxJobLv == 0:
			groups[gi].Budget = -1
		case gi == len(groups)-1 && jobLv > 0:
			groups[gi].Budget = max(0, min(jobLv, last.MaxJobLv)-1)
		default:
			groups[gi].Budget = last.MaxJobLv - 1
		}
	}
	return groups
}

// Chain returns the job's ancestry from the root (Novice) to the job itself.
func (c *Catalog) Chain(jobID int) []int {
	var chain []int
	seen := map[int]bool{}
	for id := jobID; id >= 0 && !seen[id]; {
		j, ok := c.Jobs[id]
		if !ok {
			break
		}
		seen[id] = true
		chain = append([]int{id}, chain...)
		id = j.Parent
	}
	return chain
}

// Save writes the catalog as gzipped JSON.
func (c *Catalog) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	if err := json.NewEncoder(zw).Encode(c); err != nil {
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load reads a catalog written by Save.
func Load(path string) (*Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Read reads a catalog from r: gzipped, as Save writes it, or plain JSON
// (a web server may have already unzipped it on the way).
func Read(r io.Reader) (*Catalog, error) {
	br := bufio.NewReader(r)
	var src io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("gamedata: %w", err)
		}
		src = zr
	}
	var c Catalog
	if err := json.NewDecoder(src).Decode(&c); err != nil {
		return nil, fmt.Errorf("gamedata: %w", err)
	}
	c.ApplyLatamLimits()
	return &c, nil
}

// LATAM level caps: rAthena gives 4th classes 275/60, the LATAM server
// stops at base 250 and job 50.
const (
	LatamMaxBaseLv   = 250
	LatamMaxFourthJb = 50
)

// ApplyLatamLimits caps the classes' max levels to the LATAM server's.
func (c *Catalog) ApplyLatamLimits() {
	for _, j := range c.Jobs {
		if j.MaxBaseLv > LatamMaxBaseLv {
			j.MaxBaseLv = LatamMaxBaseLv
		}
		if j.Fourth && j.MaxJobLv > LatamMaxFourthJb {
			j.MaxJobLv = LatamMaxFourthJb
		}
	}
}

var (
	colorCodeRe = regexp.MustCompile(`\^[0-9a-fA-F]{6}`)
	levelPctRe  = regexp.MustCompile(`(?i)\[\s*(?:nv|lv|niv|nível|level)\.?\s*(\d+)\s*\]\s*:\s*([\d.,]+)\s*%`)
)

// RatioAt reads the damage % for a skill level from the client description
// ("[Nv 5]: 300%"), trying PT, ES and EN. It reports false when the
// description gives no per-level percentage (formula-based skills).
func (s *Skill) RatioAt(lv int) (int, bool) {
	for _, l := range Langs {
		for _, line := range s.Desc[l] {
			line = colorCodeRe.ReplaceAllString(line, "")
			m := levelPctRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if n, _ := strconv.Atoi(m[1]); n != lv {
				continue
			}
			pct, err := strconv.Atoi(strings.NewReplacer(".", "", ",", "").Replace(m[2]))
			if err == nil && pct > 0 {
				return pct, true
			}
		}
	}
	return 0, false
}

// Pet is a pet: its monster, its egg and the bonus script it gives its owner
// (getpetinfo(PETINFO_INTIMATE) picks the tier).
type Pet struct {
	Mob    int    `json:"mob"`
	Egg    int    `json:"egg"`
	Script string `json:"script"`
}
