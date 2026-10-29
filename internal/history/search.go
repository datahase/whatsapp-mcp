package history

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

type candidate struct {
	source   Source
	semantic float64
	rank     float64
}

func (s *Service) Search(ctx context.Context, r SearchRequest) (SearchResult, error) {
	return s.search(ctx, r, "search")
}
func (s *Service) search(ctx context.Context, r SearchRequest, task string) (SearchResult, error) {
	st, err := s.authorize(ctx, r.Scope)
	if err != nil {
		return SearchResult{}, err
	}
	if len([]rune(r.Query)) < 1 || len([]rune(r.Query)) > 2000 {
		return SearchResult{}, errors.New("query must contain 1-2000 characters")
	}
	if r.Limit <= 0 {
		r.Limit = 8
	}
	if r.Limit > 20 {
		r.Limit = 20
	}
	if r.Offset < 0 || r.Offset > 180 {
		return SearchResult{}, errors.New("offset must be 0-180; narrow the search filters")
	}
	coverage, err := s.Coverage(ctx, r.Scope)
	if err != nil {
		return SearchResult{}, err
	}
	startEpoch := coverage.ContentEpoch
	startScope := coverage.ScopeEpoch
	vector, err := s.embed.Embed(ctx, []string{r.Query}, task)
	if err != nil {
		return SearchResult{}, err
	}
	if len(vector) != 1 {
		return SearchResult{}, errors.New("invalid query embedding")
	}
	where := "chat_jid IN (" + placeholders(len(r.ChatIDs)) + ") AND space=?"
	args := argsFor(r.ChatIDs)
	args = append(args, s.embed.Manifest().Key())
	if r.Sender != "" {
		where += " AND sender=?"
		args = append(args, r.Sender)
	}
	for _, f := range []struct{ v, op string }{{r.After, ">="}, {r.Before, "<="}} {
		if f.v != "" {
			t, e := time.Parse(time.RFC3339, f.v)
			if e != nil {
				return SearchResult{}, errors.New("date filters must be RFC3339 timestamps")
			}
			where += " AND ts" + f.op + "?"
			args = append(args, t.UnixMilli())
		}
	}
	rows, err := st.DB().QueryContext(ctx, "SELECT source_id,source_json,vector FROM history_docs WHERE "+where, args...)
	if err != nil {
		return SearchResult{}, err
	}
	top := []candidate{}
	for rows.Next() {
		var id, raw string
		var b []byte
		if err = rows.Scan(&id, &raw, &b); err != nil {
			rows.Close()
			return SearchResult{}, err
		}
		var src Source
		if json.Unmarshal([]byte(raw), &src) != nil {
			continue
		}
		score := dot(b, vector[0])
		if math.IsNaN(score) || math.IsInf(score, 0) {
			rows.Close()
			return SearchResult{}, errors.New("corrupt vector index; rebuild local history")
		}
		top = append(top, candidate{source: src, semantic: score})
		if len(top) > 400 {
			sort.Slice(top, func(i, j int) bool { return top[i].semantic > top[j].semantic })
			top = top[:200]
		}
		if err = checkGuard(ctx); err != nil {
			rows.Close()
			return SearchResult{}, err
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return SearchResult{}, err
	}
	rows.Close()
	sort.Slice(top, func(i, j int) bool {
		if top[i].semantic == top[j].semantic {
			return top[i].source.ID < top[j].source.ID
		}
		return top[i].semantic > top[j].semantic
	})
	if len(top) > 200 {
		top = top[:200]
	}
	byID := map[string]*candidate{}
	for i := range top {
		top[i].rank = 1.0 / float64(60+i+1)
		byID[top[i].source.ID] = &top[i]
	}
	words := strings.FieldsFunc(r.Query, func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsNumber(c) })
	terms := []string{}
	for _, w := range words {
		if len(terms) >= 12 {
			break
		}
		terms = append(terms, "\""+strings.ReplaceAll(w, "\"", "")+"\"")
	}
	if len(terms) > 0 {
		ftsArgs := append([]any{strings.Join(terms, " OR ")}, args...)
		rows, err = st.DB().QueryContext(ctx, `SELECT d.source_id,d.source_json FROM history_fts f JOIN history_docs d ON d.rowid=f.docid WHERE history_fts MATCH ? AND `+where+` ORDER BY d.ts DESC,d.source_id LIMIT 200`, ftsArgs...)
		if err != nil {
			return SearchResult{}, err
		}
		i := 0
		for rows.Next() {
			var id, raw string
			if err = rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return SearchResult{}, err
			}
			if c := byID[id]; c != nil {
				c.rank += 1.0 / float64(60+i+1)
			} else {
				var src Source
				if json.Unmarshal([]byte(raw), &src) == nil {
					byID[id] = &candidate{source: src, rank: 1.0 / float64(60+i+1)}
				}
			}
			i++
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return SearchResult{}, err
		}
		rows.Close()
	}
	ranked := []candidate{}
	for _, c := range byID {
		ranked = append(ranked, *c)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].rank == ranked[j].rank {
			return ranked[i].source.ID < ranked[j].source.ID
		}
		return ranked[i].rank > ranked[j].rank
	})
	result := SearchResult{Hits: []Hit{}, Coverage: coverage, Warnings: []string{"Ranked retrieval is not an exhaustive review; later unlinked updates can be missed. Use chronological action review for all pending actions."}}
	end := r.Offset + r.Limit
	if end > len(ranked) {
		end = len(ranked)
	}
	for i := r.Offset; i < end; i++ {
		src, e := s.validateSource(ctx, r.Scope, ranked[i].source)
		if e != nil {
			return SearchResult{}, e
		}
		neighbors, e := s.contextFor(ctx, r.Scope, src)
		if e != nil {
			return SearchResult{}, e
		}
		result.Hits = append(result.Hits, Hit{Source: src, Score: ranked[i].rank, Context: neighbors})
	}
	if end < len(ranked) {
		result.NextOffset = &end
	}
	// Never return a result assembled across edits, revocation or model changes.
	current, err := s.authorize(ctx, r.Scope)
	if err != nil || current != st || epoch(st.DB(), "content_epoch") != startEpoch || epoch(st.DB(), "scope_epoch") != startScope {
		return SearchResult{}, errors.New("history changed during search; retry with current evidence")
	}
	return result, nil
}
