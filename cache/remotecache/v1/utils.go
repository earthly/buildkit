package cacheimport

import (
	"fmt"
	"sort"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/solver"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// sortConfig sorts the config structure to make sure it is deterministic
func sortConfig(cc *CacheConfig) {
	type indexedLayer struct {
		oldIndex int
		newIndex int
		l        CacheLayer
	}

	unsortedLayers := make([]*indexedLayer, len(cc.Layers))
	sortedLayers := make([]*indexedLayer, len(cc.Layers))

	for i, l := range cc.Layers {
		il := &indexedLayer{oldIndex: i, l: l}
		unsortedLayers[i] = il
		sortedLayers[i] = il
	}
	sort.Slice(sortedLayers, func(i, j int) bool {
		li := sortedLayers[i].l
		lj := sortedLayers[j].l
		if li.Blob == lj.Blob {
			return li.ParentIndex < lj.ParentIndex
		}
		return li.Blob < lj.Blob
	})
	for i, l := range sortedLayers {
		l.newIndex = i
	}

	layers := make([]CacheLayer, len(sortedLayers))
	for i, l := range sortedLayers {
		if pID := l.l.ParentIndex; pID != -1 {
			l.l.ParentIndex = unsortedLayers[pID].newIndex
		}
		layers[i] = l.l
	}

	type indexedRecord struct {
		oldIndex int
		newIndex int
		r        CacheRecord
	}

	unsortedRecords := make([]*indexedRecord, len(cc.Records))
	sortedRecords := make([]*indexedRecord, len(cc.Records))

	for i, r := range cc.Records {
		ir := &indexedRecord{oldIndex: i, r: r}
		unsortedRecords[i] = ir
		sortedRecords[i] = ir
	}
	sort.Slice(sortedRecords, func(i, j int) bool {
		ri := sortedRecords[i].r
		rj := sortedRecords[j].r
		if ri.Digest != rj.Digest {
			return ri.Digest < rj.Digest
		}
		if len(ri.Inputs) != len(rj.Inputs) {
			return len(ri.Inputs) < len(rj.Inputs)
		}
		for i, inputs := range ri.Inputs {
			if len(ri.Inputs[i]) != len(rj.Inputs[i]) {
				return len(ri.Inputs[i]) < len(rj.Inputs[i])
			}
			for j := range inputs {
				if ri.Inputs[i][j].Selector != rj.Inputs[i][j].Selector {
					return ri.Inputs[i][j].Selector < rj.Inputs[i][j].Selector
				}
				inputDigesti := cc.Records[ri.Inputs[i][j].LinkIndex].Digest
				inputDigestj := cc.Records[rj.Inputs[i][j].LinkIndex].Digest
				if inputDigesti != inputDigestj {
					return inputDigesti < inputDigestj
				}
			}
		}
		return false
	})
	for i, l := range sortedRecords {
		l.newIndex = i
	}

	records := make([]CacheRecord, len(sortedRecords))
	for i, r := range sortedRecords {
		for j := range r.r.Results {
			r.r.Results[j].LayerIndex = unsortedLayers[r.r.Results[j].LayerIndex].newIndex
		}
		for j, inputs := range r.r.Inputs {
			for k := range inputs {
				r.r.Inputs[j][k].LinkIndex = unsortedRecords[r.r.Inputs[j][k].LinkIndex].newIndex
			}
			sort.Slice(inputs, func(i, j int) bool {
				return inputs[i].LinkIndex < inputs[j].LinkIndex
			})
		}
		records[i] = r.r
	}

	cc.Layers = layers
	cc.Records = records
}

func outputKey(dgst digest.Digest, idx int) digest.Digest {
	return digest.FromBytes([]byte(fmt.Sprintf("%s@%d", dgst, idx)))
}

type nlink struct {
	dgst     digest.Digest
	input    int
	selector string
}
type normalizeState struct {
	added map[*item]*item
	links map[*item]map[nlink]map[digest.Digest]struct{}
	byKey map[digest.Digest]*item
	next  int
}

func normalizeItem(it *item, state *normalizeState, visited map[*item]struct{}) (*item, error) {
	if visited == nil {
		return normalizeItem(it, state, make(map[*item]struct{}))
	}
	if it2, ok := state.added[it]; ok {
		return it2, nil
	}
	// if _, ok := visited[it]; ok {
	// 	return it, nil
	// }
	visited[it] = struct{}{}

	if len(it.links) == 0 {
		id := it.dgst
		if it2, ok := state.byKey[id]; ok {
			state.added[it] = it2
			return it2, nil
		}
		state.byKey[id] = it
		state.added[it] = it
		return nil, nil
	}

	matches := map[digest.Digest]struct{}{}

	// check if there is already a matching record
	for i, m := range it.links {
		if len(m) == 0 {
			return nil, errors.Errorf("invalid incomplete links")
		}
		for l := range m {
			nl := nlink{dgst: it.dgst, input: i, selector: l.selector}
			it2, err := normalizeItem(l.src, state, visited)
			if err != nil {
				return nil, err
			}
			links := state.links[it2][nl]
			if i == 0 {
				for id := range links {
					matches[id] = struct{}{}
				}
			} else {
				for id := range matches {
					if _, ok := links[id]; !ok {
						delete(matches, id)
					}
				}
			}
		}
	}

	var id digest.Digest

	links := it.links

	if len(matches) > 0 {
		for m := range matches {
			if id == "" || id > m {
				id = m
			}
		}
	} else {
		// keep tmp IDs deterministic
		state.next++
		id = digest.FromBytes([]byte(fmt.Sprintf("%d", state.next)))
		state.byKey[id] = it
		it.links = make([]map[link]struct{}, len(it.links))
		for i := range it.links {
			it.links[i] = map[link]struct{}{}
		}
	}

	it2 := state.byKey[id]
	state.added[it] = it2

	for i, m := range links {
		for l := range m {
			subIt, err := normalizeItem(l.src, state, visited)
			if err != nil {
				return nil, err
			}
			it2.links[i][link{src: subIt, selector: l.selector}] = struct{}{}

			nl := nlink{dgst: it.dgst, input: i, selector: l.selector}
			if _, ok := state.links[subIt]; !ok {
				state.links[subIt] = map[nlink]map[digest.Digest]struct{}{}
			}
			if _, ok := state.links[subIt][nl]; !ok {
				state.links[subIt][nl] = map[digest.Digest]struct{}{}
			}
			state.links[subIt][nl][id] = struct{}{}
		}
	}

	return it2, nil
}

type marshalState struct {
	layers      []CacheLayer
	chainsByID  map[string]int
	descriptors DescriptorProvider

	records       []CacheRecord
	recordsByItem map[*item]int
}

func marshalRemote(r *solver.Remote, state *marshalState) string {
	if len(r.Descriptors) == 0 {
		return ""
	}
	var parentID string
	if len(r.Descriptors) > 1 {
		r2 := &solver.Remote{
			Descriptors: r.Descriptors[:len(r.Descriptors)-1],
			Provider:    r.Provider,
		}
		parentID = marshalRemote(r2, state)
	}
	desc := r.Descriptors[len(r.Descriptors)-1]

	if desc.Digest == exptypes.EmptyGZLayer {
		return parentID
	}

	state.descriptors[desc.Digest] = DescriptorProviderPair{
		Descriptor: desc,
		Provider:   r.Provider,
	}

	id := desc.Digest.String() + parentID

	if _, ok := state.chainsByID[id]; ok {
		return id
	}

	state.chainsByID[id] = len(state.layers)
	l := CacheLayer{
		Blob:        desc.Digest,
		ParentIndex: -1,
	}
	if parentID != "" {
		l.ParentIndex = state.chainsByID[parentID]
	}
	state.layers = append(state.layers, l)
	return id
}

func marshalItem2(root *item, state *marshalState) error {
	type stackFrame struct {
		it         *item
		indexLinks int
		visitedM   map[link]struct{}
		rec        *CacheRecord
		l          *link
	}
	stack := []stackFrame{{it: root}}
	for len(stack) > 0 {
		frame := stack[0]
		if frame.rec == nil {
			frame.rec = &CacheRecord{
				Digest: frame.it.dgst,
				Inputs: make([][]CacheInput, len(frame.it.links)),
			}
		}
		if frame.visitedM == nil {
			frame.visitedM = make(map[link]struct{})
		}
		if frame.indexLinks >= len(frame.it.links) {
			if frame.it.result != nil {
				id := marshalRemote(frame.it.result, state)
				if id != "" {
					idx, ok := state.chainsByID[id]
					if !ok {
						return errors.Errorf("parent chainid not found")
					}
					frame.rec.Results = append(frame.rec.Results, CacheResult{LayerIndex: idx, CreatedAt: frame.it.resultTime})
				}
			}

			index := len(state.records)
			state.recordsByItem[frame.it] = index
			state.records = append(state.records, *frame.rec)
			if len(stack) > 1 {
				prevFrame := stack[1]
				prevFrame.rec.Inputs[prevFrame.indexLinks] = append(
					prevFrame.rec.Inputs[prevFrame.indexLinks],
					CacheInput{
						Selector:  prevFrame.l.selector,
						LinkIndex: index,
					})
			}
			// Pop.
			stack = stack[1:]
			continue
		}

		m := frame.it.links[frame.indexLinks]
		// TODO: This would be easier if m was a list, not a map.
		frame.l = nil
		for l := range m {
			_, found := frame.visitedM[l]
			if !found {
				frame.l = &l
				break
			}
		}
		if frame.l == nil {
			// All visited.
			frame.indexLinks++
			continue
		}
		frame.visitedM[*frame.l] = struct{}{}

		stack = append(stack, stackFrame{it: frame.l.src})
	}
	return nil
}

func marshalItem(it *item, state *marshalState, depth int, maxDepth *int, visited map[*item]bool) error {
	if depth > *maxDepth {
		fmt.Printf("new max depth: %d\n", depth)
		*maxDepth = depth
	}
	if _, ok := state.recordsByItem[it]; ok {
		return nil
	}
	if visited[it] {
		panic("visited!!")
	}
	visited[it] = true

	rec := CacheRecord{
		Digest: it.dgst,
		Inputs: make([][]CacheInput, len(it.links)),
	}
	// state.recordsByItem[it] = len(state.records)
	// state.records = append(state.records, rec)

	for i, m := range it.links {
		for l := range m {
			if err := marshalItem(l.src, state, depth+1, maxDepth, visited); err != nil {
				return err
			}
			idx, ok := state.recordsByItem[l.src]
			if !ok {
				return errors.Errorf("invalid source record: %v", l.src)
			}
			rec.Inputs[i] = append(rec.Inputs[i], CacheInput{
				Selector:  l.selector,
				LinkIndex: idx,
			})
		}
	}

	if it.result != nil {
		id := marshalRemote(it.result, state)
		if id != "" {
			idx, ok := state.chainsByID[id]
			if !ok {
				return errors.Errorf("parent chainid not found")
			}
			rec.Results = append(rec.Results, CacheResult{LayerIndex: idx, CreatedAt: it.resultTime})
		}
	}

	state.recordsByItem[it] = len(state.records)
	state.records = append(state.records, rec)

	return nil
}

func isSubRemote(sub, main solver.Remote) bool {
	if len(sub.Descriptors) > len(main.Descriptors) {
		return false
	}
	for i := range sub.Descriptors {
		if sub.Descriptors[i].Digest != main.Descriptors[i].Digest {
			return false
		}
	}
	return true
}
