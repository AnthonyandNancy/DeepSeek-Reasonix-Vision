package control

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/vision"
)

type MediaTurnResolution struct {
	Images              []ResolvedImage
	ReanalysisRequested bool
}

func (r MediaTurnResolution) applyReanalysisGuidance(input string, toolAvailable bool) string {
	if !r.ReanalysisRequested {
		return input
	}
	block := `<visual-reanalysis-status>
A fresh visual analysis is unavailable because the independent visual analysis tool could not be started.
Do not claim that a fresh analysis was performed; explain that fresh pixel analysis is unavailable.
</visual-reanalysis-status>`
	if toolAvailable {
		block = `<visual-reanalysis-request>
The user requested a fresh analysis of media already stored in this conversation.
The main model must call analyze_media_with_vision before answering; the tool selects conversation-owned media and returns ModLens v2 evidence.
Do not infer pixels from a filename or path, and do not present older evidence as a fresh analysis.
</visual-reanalysis-request>`
	}
	if strings.TrimSpace(input) == "" {
		return block
	}
	return block + "\n\n" + input
}

func (c *Controller) routeResolvedMediaOnce(ctx context.Context, state *ImageRouteState, input, rawQuestion string, media MediaTurnResolution) ImageRouteResult {
	if state == nil {
		state = &ImageRouteState{}
	}
	state.RequireIndependentVision = media.ReanalysisRequested
	route := c.routeImagesOnce(ctx, state, input, rawQuestion, media.Images)
	toolAvailable := c != nil && c.visionDescriber != nil && c.visionModelRefOr() != ""
	route.Input = c.injectVisualModelAssistance(media.applyReanalysisGuidance(route.Input, toolAvailable))
	return route
}

func (c *Controller) resolveMediaForTurn(input string) MediaTurnResolution {
	images := c.resolveInputImages(input)
	if len(images) > 0 {
		return MediaTurnResolution{Images: dedupeResolvedImages(images)}
	}
	if !visionReanalysisRequested(input) {
		return MediaTurnResolution{}
	}
	return MediaTurnResolution{ReanalysisRequested: true}
}

func (c *Controller) ResolveHistoricalVisionMedia(_ context.Context, selection vision.MediaSelection) ([]vision.Image, []string, error) {
	groups := c.safeHistoricalMediaGroups()
	resolved := selectHistoricalVisionMedia(groups, selection)
	if len(resolved) == 0 {
		return nil, nil, nil
	}
	images := toVisionImages(resolved)
	refs := make([]string, 0, len(images))
	for _, image := range images {
		ref := strings.TrimSpace(image.Ref)
		if ref == "" {
			ref = strings.TrimSpace(filepath.ToSlash(image.Path))
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return images, refs, nil
}

func (c *Controller) safeHistoricalMediaGroups() [][]ResolvedImage {
	var groups [][]ResolvedImage
	for messageIndex, message := range c.History() {
		group := storedConversationImages(message, messageIndex)
		if len(group) == 0 && message.Role == provider.RoleUser {
			sources := historicalMessageSources(message)
			for _, candidate := range historicalMessageSources(message) {
				if display := strings.TrimSpace(c.resolveHistoricalUserContent(candidate)); display != "" {
					sources = append(sources, display)
				}
			}
			for _, ref := range message.MediaRefs {
				sources = append(sources, ref)
			}
			for _, source := range sources {
				for _, image := range c.resolveHistoricalAttachmentPaths(source) {
					image.Ref = strings.TrimPrefix(strings.TrimSpace(image.Ref), "@")
					group = append(group, image)
				}
			}
		}
		group = dedupeResolvedImages(group)
		if len(group) > 0 {
			groups = append(groups, group)
		}
	}
	return groups
}

func storedConversationImages(message provider.Message, messageIndex int) []ResolvedImage {
	group := make([]ResolvedImage, 0, len(message.Images))
	for imageIndex, dataURL := range message.Images {
		if strings.TrimSpace(dataURL) == "" {
			continue
		}
		ref := ""
		if imageIndex < len(message.MediaRefs) {
			candidate := strings.TrimPrefix(strings.TrimSpace(message.MediaRefs[imageIndex]), "@")
			if normalized, ok := normalizeHistoricalAttachmentPath(candidate); ok {
				ref = normalized
			}
		}
		if ref == "" {
			ref = fmt.Sprintf("conversation:%d:%d", messageIndex, imageIndex)
		}
		group = append(group, ResolvedImage{Ref: ref, DataURL: dataURL})
	}
	return group
}

func selectHistoricalVisionMedia(groups [][]ResolvedImage, selection vision.MediaSelection) []ResolvedImage {
	if len(groups) == 0 {
		return nil
	}
	if selection.All {
		var all []ResolvedImage
		for _, group := range groups {
			all = append(all, group...)
		}
		return dedupeResolvedImages(all)
	}
	if selection.Index >= 0 {
		var all []ResolvedImage
		for _, group := range groups {
			all = append(all, group...)
		}
		all = dedupeResolvedImages(all)
		if selection.Index >= len(all) {
			return nil
		}
		return []ResolvedImage{all[selection.Index]}
	}
	latest := groups[len(groups)-1]
	return []ResolvedImage{latest[len(latest)-1]}
}

func visionReanalysisRequested(input string) bool {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return false
	}
	if containsAny(text, "what does reanalyze mean", "what does re-analyze mean", "what is reanalyze", "meaning of reanalyze", "重新分析是什么意思", "什么叫重新分析", "reanalyze 是什么意思") {
		return false
	}
	if negatesVisionReanalysis(text) {
		return false
	}
	for _, term := range []string{
		"不要重新分析", "不要再分析", "无需重新分析", "不需要重新分析", "为什么没有重新分析", "为什么没重新分析",
		"why didn't you reanalyze", "why did not you reanalyze", "why didn't it reanalyze",
	} {
		if strings.Contains(text, term) {
			return false
		}
	}
	for _, term := range []string{
		"重新分析", "重新识别", "重新读取", "再分析", "再识别", "重新看", "重新查看", "重新调用视觉模型",
		"analyze again", "reanalyze", "re-analyze", "reinspect", "re-inspect", "look again", "read again",
	} {
		if strings.Contains(text, term) {
			return true
		}
	}
	again := strings.Contains(text, " again") || strings.Contains(text, "one more time") || strings.Contains(text, "once more")
	media := containsAny(text, "image", "picture", "photo", "screenshot", "media")
	action := containsAny(text, "analyze", "analyse", "inspect", "look", "read", "check", "describe", "recognize")
	return again && media && action
}

func negatesVisionReanalysis(text string) bool {
	text = strings.ToLower(strings.NewReplacer("’", "'", "‐", "-", "‑", "-", "–", " ", "—", " ").Replace(text))
	actions := map[string]struct{}{
		"reanalyze": {}, "re-analyze": {}, "analyze": {}, "analyse": {}, "inspect": {}, "reinspect": {}, "re-inspect": {}, "look": {}, "read": {}, "check": {},
	}
	gerunds := map[string]struct{}{
		"reanalyzing": {}, "re-analyzing": {}, "analyzing": {}, "analysing": {}, "inspecting": {}, "reinspecting": {}, "re-inspecting": {}, "looking": {}, "reading": {}, "checking": {},
	}
	modifiers := map[string]struct{}{
		"again": {}, "actually": {}, "ever": {}, "just": {}, "now": {}, "please": {}, "really": {},
	}
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune(".;!?\n\r", r)
	}) {
		rawTokens := strings.Fields(clause)
		tokens := make([]string, 0, len(rawTokens))
		for _, token := range rawTokens {
			if token = strings.Trim(token, ",:\"'()[]{}<>"); token != "" {
				tokens = append(tokens, token)
			}
		}
		for index := 0; index < len(tokens); index++ {
			start := -1
			candidates := actions
			switch {
			case tokens[index] == "don't" || tokens[index] == "dont" || tokens[index] == "never":
				start = index + 1
			case tokens[index] == "do" && index+1 < len(tokens) && tokens[index+1] == "not":
				start = index + 2
			case tokens[index] == "without":
				start = index + 1
				candidates = gerunds
			}
			for offset := 0; start >= 0 && start+offset < len(tokens) && offset <= 3; offset++ {
				token := tokens[start+offset]
				if _, ok := candidates[token]; ok {
					return true
				}
				if _, ok := modifiers[token]; !ok {
					break
				}
			}
		}
	}
	return false
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func historicalMessageSources(message provider.Message) []string {
	return []string{
		strings.TrimSpace(message.RawContent),
		strings.TrimSpace(message.Content),
		strings.TrimSpace(message.ProviderContent),
	}
}

func (c *Controller) resolveHistoricalAttachmentPaths(text string) []ResolvedImage {
	paths := historicalAttachmentPaths(text)
	if len(paths) == 0 {
		return nil
	}
	out := make([]ResolvedImage, 0, len(paths))
	for _, path := range paths {
		image := ResolvedImage{Ref: "@" + path, Path: path}
		dataURL, err := visionFileImageDataURL(path, c.workspaceRoot)
		if err != nil {
			image.Error = "image file unreadable or unsupported"
		} else {
			image.DataURL = dataURL
		}
		out = append(out, image)
	}
	return out
}

func historicalAttachmentPaths(text string) []string {
	textLower := strings.ToLower(text)
	const (
		slashMarker     = ".reasonix/attachments/"
		backslashMarker = ".reasonix\\attachments\\"
	)
	var out []string
	seen := map[string]struct{}{}
	for offset := 0; offset < len(text); {
		idx, markerLen := nextHistoricalAttachmentMarker(textLower[offset:], slashMarker, backslashMarker)
		if idx < 0 {
			break
		}
		idx += offset
		end := idx + markerLen
		for end < len(text) && !historicalAttachmentBoundary(text[end]) {
			end++
		}
		path, ok := normalizeHistoricalAttachmentPath(strings.TrimRight(text[idx:end], ".,;!?，。；！？"))
		if ok && isImageAttachmentRef(path) {
			if _, ok := seen[path]; !ok {
				seen[path] = struct{}{}
				out = append(out, path)
			}
		}
		offset = max(end, idx+markerLen)
	}
	return out
}

func normalizeHistoricalAttachmentPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	root := filepath.Join(".reasonix", "attachments")
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(clean), true
}

func nextHistoricalAttachmentMarker(text, slashMarker, backslashMarker string) (index, length int) {
	slash := strings.Index(text, slashMarker)
	backslash := strings.Index(text, backslashMarker)
	switch {
	case slash < 0:
		return backslash, len(backslashMarker)
	case backslash < 0:
		return slash, len(slashMarker)
	case slash < backslash:
		return slash, len(slashMarker)
	default:
		return backslash, len(backslashMarker)
	}
}

func historicalAttachmentBoundary(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r', '\n', '"', '\'', '<', '>', '(', ')', '[', ']', ',', ';', '!', '?':
		return true
	default:
		return false
	}
}

func dedupeResolvedImages(images []ResolvedImage) []ResolvedImage {
	seen := make(map[string]struct{}, len(images))
	out := make([]ResolvedImage, 0, len(images))
	for _, image := range images {
		key := strings.TrimSpace(image.DataURL)
		if key == "" {
			key = strings.TrimSpace(image.Path) + "\x00" + strings.TrimSpace(image.Ref)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, image)
	}
	return out
}

func mediaRefsForResolvedImages(images []ResolvedImage) []string {
	refs := make([]string, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		ref := strings.TrimSpace(image.Ref)
		if strings.HasPrefix(ref, "history:") || strings.HasPrefix(strings.ToLower(ref), "data:") {
			ref = ""
		}
		if ref == "" {
			ref = strings.TrimSpace(filepath.ToSlash(image.Path))
		}
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs
}
