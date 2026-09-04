package handlers

import "strings"

// productTaxonomy mirrors cutmax-frontend's src/lib/taxonomy.ts (kept as a
// separate copy rather than shared, since the two repos deploy
// independently). Bulk imports map a supplier sheet's raw category/
// sub-category text onto these exact strings -- an unmapped raw value like
// "ENDMILL" never matching the storefront's real sub-category name "Flat
// Endmill" is what "sub-categories aren't updating on the site" turned out
// to mean in practice: the product imported fine, it just never matched any
// filter/tab.
var productTaxonomy = []struct {
	Name          string
	SubCategories []string
}{
	{"Carbide Inserts", []string{"Turning Inserts", "Milling Inserts", "Drilling Inserts", "Grooving Inserts", "Threading Inserts"}},
	{"End Mills", []string{"Flat Endmill", "Ball Nose", "Corner Radius", "Long Neck"}},
	{"Tool Holders & Adapters", []string{"Turning Tool Holders", "Boring Tool Holders", "Grooving Tool Holders", "Milling Tool Holders", "Threading Tool Holders"}},
	{"Milling Cutters & Adapters", []string{"Face Milling Cutters", "End Mill Cutters", "High Feed Cutters", "Indexable End Mills", "Adapters"}},
	{"Spares", []string{"Top Clamps", "Screws", "Torx", "Shim", "Shim Pin", "Shim Screw", "Allen Keys"}},
	{"Others", []string{"Direct Enquiry"}},
	{"Special Tools", []string{"Direct Enquiry"}},
}

var categoryByKey = map[string]string{}
var subCategoryByKey = map[string]string{}

func init() {
	for _, c := range productTaxonomy {
		categoryByKey[taxonomyKey(c.Name)] = c.Name
		for _, s := range c.SubCategories {
			subCategoryByKey[taxonomyKey(s)] = s
		}
	}
}

func taxonomyKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "-", "", "_", "", "&", "").Replace(s)
}

// insertTypeAliases recognizes a supplier sheet naming an insert type
// directly as the category (e.g. "TURNING INSERT") instead of the
// storefront's two-level "Carbide Inserts > Turning Inserts" split -- the
// canonical sub-category comes from the CATEGORY column here, not the
// sheet's own sub-category column (see materialAliases below for why).
var insertTypeAliases = map[string]string{
	"turninginsert":   "Turning Inserts",
	"millinginsert":   "Milling Inserts",
	"milinginsert":    "Milling Inserts", // common sheet typo (MILING)
	"drillinginsert":  "Drilling Inserts",
	"groovinginsert":  "Grooving Inserts",
	"threadinginsert": "Threading Inserts",
}

var endmillCategoryAliases = map[string]bool{
	"endmill": true, "endmills": true, "endmil": true,
}

var endmillSubAliases = map[string]string{
	"flatendmill":    "Flat Endmill",
	"bollnos":        "Ball Nose", // sheet typo
	"ballnos":        "Ball Nose",
	"ballnose":       "Ball Nose",
	"cornerreadious": "Corner Radius", // sheet typo
	"cornerradius":   "Corner Radius",
	"longnick":       "Long Neck", // sheet typo
	"longneck":       "Long Neck",
}

// materialAliases: for insert-type rows, a supplier sheet's "sub-category"
// column is actually the carbide grade (CARBIDE/CERMET/PCD/...), not a real
// sub-category -- normalizeTaxonomy maps it onto the material field instead.
var materialAliases = map[string]string{
	"carbide": "Carbide",
	"cermet":  "Cermet",
	"pcd":     "PCD",
	"other":   "Other",
	"othr":    "Other",
}

func titleCase(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// normalizeTaxonomy maps a raw (category, subCategory) pair from an import
// sheet onto the site's canonical taxonomy. It returns the resolved
// category, sub-category, an optional material grade (only ever set for
// insert-type rows), and whether the input needed a best-effort fallback
// the admin should double check -- an unrecognized raw value gets
// title-cased and used as-is rather than silently dropped, but is flagged.
func normalizeTaxonomy(rawCategory, rawSubCategory string) (category, subCategory, material string, needsReview bool) {
	catKey := taxonomyKey(rawCategory)
	subKey := taxonomyKey(rawSubCategory)

	if sub, ok := insertTypeAliases[catKey]; ok {
		mat := materialAliases[subKey]
		if mat == "" && rawSubCategory != "" {
			mat = titleCase(rawSubCategory)
			needsReview = true
		}
		return "Carbide Inserts", sub, mat, needsReview
	}

	if endmillCategoryAliases[catKey] {
		if sub, ok := endmillSubAliases[subKey]; ok {
			return "End Mills", sub, "", false
		}
		return "End Mills", titleCase(rawSubCategory), "", rawSubCategory != ""
	}

	if canon, ok := categoryByKey[catKey]; ok {
		if canonSub, ok := subCategoryByKey[subKey]; ok {
			return canon, canonSub, "", false
		}
		return canon, titleCase(rawSubCategory), "", rawSubCategory != ""
	}

	if rawCategory == "" {
		return "", titleCase(rawSubCategory), "", false
	}
	return titleCase(rawCategory), titleCase(rawSubCategory), "", true
}
