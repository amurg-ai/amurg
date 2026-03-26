package promptprofile

import "strings"

const DefaultID = "standard"

type Profile struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt,omitempty"`
}

type Metadata struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

var orderedProfiles = []Profile{
	{
		ID:          "standard",
		DisplayName: "Standard",
		Description: "Balanced framing for general collaboration and everyday tasks.",
		Prompt:      "You are a skilled collaborator working alongside the user. Balance speed with rigor, make pragmatic decisions, and keep your reasoning concise and actionable.",
	},
	{
		ID:          "deep_debug",
		DisplayName: "Deep Debug",
		Description: "Trust-maximizing framing for investigation, debugging, and root-cause analysis.",
		Prompt:      "You have deep expertise in this codebase. Approach this investigation thoroughly, look past surface symptoms to find root causes, and optimize for correctness and understanding over speed.",
	},
	{
		ID:          "careful",
		DisplayName: "Careful",
		Description: "Constraint-aware framing for sensitive production, security, or migration work.",
		Prompt:      "This is a sensitive operation. Consider side effects carefully, validate assumptions explicitly, and flag anything that could affect production stability, security, or data integrity before acting.",
	},
	{
		ID:          "fast",
		DisplayName: "Fast",
		Description: "Minimal framing for quick, well-scoped tasks with little extra exploration.",
		Prompt:      "Complete the specified task efficiently. Keep exploration to the minimum needed to finish the task correctly.",
	},
}

var profilesByID = func() map[string]Profile {
	profiles := make(map[string]Profile, len(orderedProfiles))
	for _, profile := range orderedProfiles {
		profiles[profile.ID] = profile
	}
	return profiles
}()

func Normalize(id string) string {
	if strings.TrimSpace(id) == "" {
		return DefaultID
	}
	return strings.TrimSpace(id)
}

func Lookup(id string) (Profile, bool) {
	profile, ok := profilesByID[Normalize(id)]
	return profile, ok
}

func List() []Profile {
	out := make([]Profile, len(orderedProfiles))
	copy(out, orderedProfiles)
	return out
}

func ListMetadata() []Metadata {
	out := make([]Metadata, 0, len(orderedProfiles))
	for _, profile := range orderedProfiles {
		out = append(out, Metadata{
			ID:          profile.ID,
			DisplayName: profile.DisplayName,
			Description: profile.Description,
		})
	}
	return out
}

func Append(base, profileID string) string {
	profile, ok := Lookup(profileID)
	trimmedBase := strings.TrimSpace(base)
	if !ok {
		return trimmedBase
	}

	parts := make([]string, 0, 2)
	if prompt := strings.TrimSpace(profile.Prompt); prompt != "" {
		parts = append(parts, prompt)
	}
	if trimmedBase != "" {
		parts = append(parts, trimmedBase)
	}
	return strings.Join(parts, "\n\n")
}
