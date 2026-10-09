package prompts

import (
	"github.com/bdobrica/SecondContext/internal/knowledge"
)

// UTF-8 bytes give a deliberately conservative token upper bound for byte-based
// tokenizers without coupling the Go consumer to an upstream model's tokenizer.
// These limits cover rendered retrieved sections, including labels/provenance;
// user input, instructions and fixed prompt rules have separate request limits.
const RetrievedContextTokenBudget = 6000

var sectionTokenBudgets = map[string]int{
	"knowledge": 3000, "memory": 1800, "people": 400, "topics": 400, "beliefs": 400,
}

type ContextBudget struct {
	Method   string         `json:"method"`
	Limit    int            `json:"limit"`
	Used     int            `json:"used"`
	Sections map[string]int `json:"sections"`
}

// ApplyContextBudgets arbitrates fixed reservations: one category cannot evict
// another. Empty reservations stay unused. Full citation fields always survive;
// an evidence item whose provenance alone cannot fit is omitted.
func ApplyContextBudgets(p *ContextPacket) {
	if p == nil {
		return
	}
	original := p.KnowledgeContext
	p.KnowledgeContext = nil
	for _, item := range original {
		p.KnowledgeContext = append(p.KnowledgeContext, item)
		if len(buildKnowledgeSection(p)) <= sectionTokenBudgets["knowledge"] {
			continue
		}
		index := len(p.KnowledgeContext) - 1
		text := item.Text
		low, high := 0, len(text)
		for low < high {
			mid := (low + high + 1) / 2
			p.KnowledgeContext[index].Text = knowledge.ClipUTF8(text, mid)
			p.KnowledgeContext[index].Truncated = true
			if len(buildKnowledgeSection(p)) <= sectionTokenBudgets["knowledge"] {
				low = mid
			} else {
				high = mid - 1
			}
		}
		p.KnowledgeContext[index].Text = knowledge.ClipUTF8(text, low)
		p.KnowledgeContext[index].Truncated = true
		if p.KnowledgeContext[index].Text == "" || len(buildKnowledgeSection(p)) > sectionTokenBudgets["knowledge"] {
			p.KnowledgeContext = p.KnowledgeContext[:index]
			p.OmittedKnowledge++
		}
	}
	for len(p.MemoryContext) > 0 && len(buildMemorySection(p)) > sectionTokenBudgets["memory"] {
		p.MemoryContext = p.MemoryContext[:len(p.MemoryContext)-1]
		p.OmittedMemories++
	}
	trimLines(&p.PeopleContext, sectionTokenBudgets["people"], func() string { return buildPeopleSection(p) })
	trimLines(&p.TopicContext, sectionTokenBudgets["topics"], func() string { return buildTopicSection(p) })
	trimLines(&p.BeliefContext, sectionTokenBudgets["beliefs"], func() string { return buildBeliefSection(p) })
	usage := map[string]int{"knowledge": len(buildKnowledgeSection(p)), "memory": len(buildMemorySection(p)), "people": len(buildPeopleSection(p)), "topics": len(buildTopicSection(p)), "beliefs": len(buildBeliefSection(p))}
	p.ContextBudget = &ContextBudget{Method: "utf8_bytes_token_upper_bound", Limit: RetrievedContextTokenBudget, Sections: usage}
	for _, used := range usage {
		p.ContextBudget.Used += used
	}
}
func trimLines(lines *[]string, budget int, render func() string) {
	for len(*lines) > 0 && len(render()) > budget {
		index := len(*lines) - 1
		excess := len(render()) - budget
		text := (*lines)[index]
		if len(text) <= excess {
			*lines = (*lines)[:index]
		} else {
			(*lines)[index] = knowledge.ClipUTF8(text, len(text)-excess)
		}
	}
}
