package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/Usefused/cli/internal/api"
)

type serviceNotFoundError struct {
	query string
}

// Error retains the existing diagnostic for callers without useful suggestions.
func (err *serviceNotFoundError) Error() string {
	return fmt.Sprintf("service %q was not found in the workspace or Registry", err.query)
}

// withServiceSuggestions performs one bounded, read-only fallback search after
// a genuine miss; suggestions never re-enter service selection or activation.
func withServiceSuggestions(client *api.Client, err error) error {
	var missing *serviceNotFoundError
	// Only an absent service justifies broader discovery, never a denied lookup.
	if !errors.As(err, &missing) {
		return err
	}
	queries := serviceSuggestionQueries(missing.query)
	// Empty and very short input must not cause an unbounded catalogue search.
	if len(queries) == 0 {
		return err
	}
	results, searchErr := client.SearchServicesBatch(queries)
	// Hints are optional: a failed secondary lookup must preserve the original error.
	if searchErr != nil {
		return err
	}
	var candidates []api.Service
	for _, query := range queries {
		candidates = append(candidates, results[query]...)
	}
	suggestions := rankServiceSuggestions(missing.query, candidates)
	// An unrelated search hit is less useful than the unchanged miss diagnostic.
	if len(suggestions) == 0 {
		return err
	}
	for index, suggestion := range suggestions {
		suggestions[index] = strconv.Quote(suggestion)
	}
	return fmt.Errorf("%w. Did you mean %s?", err, strings.Join(suggestions, ", "))
}

// serviceSuggestionQueries searches a readable phrase plus short fragments,
// allowing spelling errors while limiting the fallback to three sets of 20 hits.
func serviceSuggestionQueries(query string) []string {
	words := serviceSuggestionWords(api.ServiceLookupName(query))
	// Tiny inputs are too ambiguous to justify extra discovery requests.
	if len([]rune(strings.Join(words, ""))) < 3 {
		return nil
	}
	first, last := []rune(words[0]), []rune(words[len(words)-1])
	fragments := []string{strings.Join(words, " "), string(first[:min(3, len(first))]), string(last[:min(3, len(last))])}
	// A suffix can recover a typo at the start of a single-word service name.
	if len(words) == 1 {
		fragments[2] = string(last[max(0, len(last)-3):])
	}
	var queries []string
	seen := make(map[string]bool)
	for _, fragment := range fragments {
		// Deduplication and a minimum fragment length keep the fallback bounded.
		if len([]rune(fragment)) >= 3 && !seen[fragment] {
			queries = append(queries, fragment)
			seen[fragment] = true
		}
	}
	return queries
}

// rankServiceSuggestions offers only close, reusable identities actually returned
// by the authenticated Registry search, preserving any requested provider scope.
func rankServiceSuggestions(query string, candidates []api.Service) []string {
	reference := api.ParseServiceReference(query)
	needle := []rune(strings.Join(serviceSuggestionWords(reference.Slug), ""))
	// Very short names have too many coincidental edit-distance matches.
	if len(needle) < 3 {
		return nil
	}
	maxDistance := 1
	// Longer names can tolerate two edits, including adjacent transpositions.
	if len(needle) >= 6 {
		maxDistance = 2
	}
	type scoredSuggestion struct {
		slug     string
		distance int
	}
	var scored []scoredSuggestion
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		parsed := api.ParseServiceReference(candidate.Slug)
		// Missing or malformed identities cannot produce a reusable suggestion.
		if candidate.ID == "" || candidate.Slug == "" || len(candidate.Slug) > 128 ||
			strings.IndexFunc(candidate.Slug, unsafeSuggestionRune) >= 0 ||
			(parsed.ProviderPrefixed && !parsed.Qualified) {
			continue
		}
		slug := candidate.DisplaySlug()
		// A qualified typo must not suggest an unrelated provider's service.
		if reference.Qualified {
			// Ownership may otherwise hide the provider in DisplaySlug.
			if candidate.Provider == nil || !strings.EqualFold(reference.Provider, candidate.Provider.Handle) {
				continue
			}
			slug = "@" + candidate.Provider.Handle + "/" + candidate.Slug
		}
		// Provider handles also come from the response and must be safe to display.
		if strings.IndexFunc(slug, unsafeSuggestionRune) >= 0 || seen[slug] {
			continue
		}
		distance := min(
			serviceSuggestionDistance(needle, []rune(strings.Join(serviceSuggestionWords(parsed.Slug), ""))),
			serviceSuggestionDistance(needle, []rune(strings.Join(serviceSuggestionWords(candidate.Name), ""))),
		)
		// Broad lexical fragments only discover candidates; closeness decides hints.
		if distance <= maxDistance {
			scored = append(scored, scoredSuggestion{slug: slug, distance: distance})
			seen[slug] = true
		}
	}
	// Stable lexical ties make hints independent of response and map ordering.
	sort.Slice(scored, func(i, j int) bool {
		// Prefer the closest spelling before comparing equally useful references.
		if scored[i].distance != scored[j].distance {
			return scored[i].distance < scored[j].distance
		}
		return scored[i].slug < scored[j].slug
	})
	var suggestions []string
	for _, candidate := range scored[:min(3, len(scored))] {
		suggestions = append(suggestions, candidate.slug)
	}
	return suggestions
}

// serviceSuggestionWords removes separator and case differences while bounding
// the memory and edit-distance work for user input and Registry display names.
func serviceSuggestionWords(value string) []string {
	// Oversized names are left without hints rather than partially guessed.
	if len(value) > 128 {
		return nil
	}
	return strings.FieldsFunc(strings.ToLower(value), serviceSuggestionSeparator)
}

// serviceSuggestionSeparator treats punctuation as a word boundary for lexical search.
func serviceSuggestionSeparator(char rune) bool {
	return !unicode.IsLetter(char) && !unicode.IsDigit(char)
}

// unsafeSuggestionRune excludes whitespace and terminal controls from reusable references.
func unsafeSuggestionRune(char rune) bool {
	return unicode.IsSpace(char) || unicode.IsControl(char)
}

// serviceSuggestionDistance measures spelling changes with a single dynamic
// programming row; both inputs have already passed the bounded normalization.
func serviceSuggestionDistance(left, right []rune) int {
	row := make([]int, len(right)+1)
	for index := range row {
		row[index] = index
	}
	for i, leftChar := range left {
		diagonal := row[0]
		row[0] = i + 1
		for j, rightChar := range right {
			previous := row[j+1]
			cost := 0
			// Equal characters need no edit; a mismatch requires substitution.
			if leftChar != rightChar {
				cost = 1
			}
			row[j+1] = min(row[j]+1, previous+1, diagonal+cost)
			diagonal = previous
		}
	}
	return row[len(right)]
}
