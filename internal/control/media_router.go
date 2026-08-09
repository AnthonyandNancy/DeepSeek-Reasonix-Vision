package control

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"reasonix/internal/provider"
)

type MediaTurnResolution struct {
	Images              []ResolvedImage
	ReanalysisRequested bool
	RecoveryError       string
}

func (r MediaTurnResolution) applyStatus(input string) string {
	if strings.TrimSpace(r.RecoveryError) == "" {
		return input
	}
	block := `<visual-reanalysis-status>
The user requested a fresh visual analysis. No recoverable historical media was available.
Do not claim that a new visual analysis was performed; explain that the original media could not be recovered.
</visual-reanalysis-status>`
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
	route.Input = c.injectVisualModelAssistance(media.applyStatus(route.Input))
	return route
}

// resolveMediaForTurn includes the latest historical media only for an
// explicit re-analysis request. Ordinary follow-up questions continue to use
// the evidence already present in the session.
func (c *Controller) resolveMediaForTurn(input string) MediaTurnResolution {
	images := c.resolveInputImages(input)
	if len(images) > 0 {
		return MediaTurnResolution{Images: dedupeResolvedImages(images)}
	}
	if !visionReanalysisRequested(input) {
		return MediaTurnResolution{}
	}
	images = c.resolveHistoricalMedia(historicalMediaSelectionFor(input))
	if len(images) == 0 {
		return MediaTurnResolution{ReanalysisRequested: true, RecoveryError: "historical media unavailable"}
	}
	return MediaTurnResolution{Images: images, ReanalysisRequested: true}
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

type historicalMediaSelection struct {
	all   bool
	index int
}

func historicalMediaSelectionFor(input string) historicalMediaSelection {
	text := strings.ToLower(input)
	if containsAny(text, "所有图片", "全部图片", "所有截图", "全部截图", "all images", "all screenshots", "every image", "every screenshot") {
		return historicalMediaSelection{all: true, index: -1}
	}
	ordinalTerms := [][]string{
		{"第一张", "首张", "first image", "first screenshot", "first picture", "first photo"},
		{"第二张", "second image", "second screenshot", "second picture", "second photo"},
		{"第三张", "third image", "third screenshot", "third picture", "third photo"},
		{"第四张", "fourth image", "fourth screenshot", "fourth picture", "fourth photo"},
		{"第五张", "fifth image", "fifth screenshot", "fifth picture", "fifth photo"},
		{"第六张", "sixth image", "sixth screenshot", "sixth picture", "sixth photo"},
		{"第七张", "seventh image", "seventh screenshot", "seventh picture", "seventh photo"},
		{"第八张", "eighth image", "eighth screenshot", "eighth picture", "eighth photo"},
		{"第九张", "ninth image", "ninth screenshot", "ninth picture", "ninth photo"},
		{"第十张", "tenth image", "tenth screenshot", "tenth picture", "tenth photo"},
	}
	for index, terms := range ordinalTerms {
		if containsAny(text, terms...) {
			return historicalMediaSelection{index: index}
		}
	}
	return historicalMediaSelection{index: -1}
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func (c *Controller) resolveHistoricalMedia(selection historicalMediaSelection) []ResolvedImage {
	groups := c.historicalMediaGroups()
	if len(groups) == 0 {
		return nil
	}
	if selection.all {
		var all []ResolvedImage
		for _, group := range groups {
			all = append(all, group...)
		}
		return dedupeResolvedImages(all)
	}
	if selection.index >= 0 {
		var all []ResolvedImage
		for _, group := range groups {
			all = append(all, group...)
		}
		all = dedupeResolvedImages(all)
		if selection.index >= len(all) {
			return nil
		}
		return []ResolvedImage{all[selection.index]}
	}
	return groups[len(groups)-1]
}

func (c *Controller) historicalMediaGroups() [][]ResolvedImage {
	var groups [][]ResolvedImage
	for index, message := range c.History() {
		var group []ResolvedImage
		for imageIndex, dataURL := range message.Images {
			if strings.TrimSpace(dataURL) == "" {
				continue
			}
			group = append(group, ResolvedImage{
				Ref:     "history:" + strconv.Itoa(index) + ":" + strconv.Itoa(imageIndex),
				DataURL: dataURL,
			})
		}
		if len(group) == 0 {
			sources := historicalMessageSources(message)
			for _, ref := range message.MediaRefs {
				if ref = strings.TrimSpace(ref); ref != "" {
					sources = append(sources, ref)
				}
			}
			// Desktop's .display.json is a legacy UI-side source. It is
			// intentionally consulted only for user turns; assistant/tool text
			// must never be allowed to redefine the user's authored prompt.
			if message.Role == provider.RoleUser {
				for _, candidate := range historicalMessageSources(message) {
					if display := strings.TrimSpace(c.resolveHistoricalUserContent(candidate)); display != "" {
						sources = append(sources, display)
					}
				}
			}
			for _, source := range sources {
				if images := c.resolveInputImages(source); len(images) > 0 {
					group = append(group, images...)
				}
				if images := c.resolveHistoricalAttachmentPaths(source); len(images) > 0 {
					group = append(group, images...)
				}
				if images := c.resolveHistoricalAbsoluteImagePaths(source); len(images) > 0 {
					group = append(group, images...)
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

// resolveHistoricalAbsoluteImagePaths recovers workspace-scoped image paths
// emitted by tools such as glob/read_file; the attachment reader validates them.
func (c *Controller) resolveHistoricalAbsoluteImagePaths(text string) []ResolvedImage {
	if c == nil || strings.TrimSpace(c.workspaceRoot) == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []ResolvedImage
	for _, candidate := range historicalAbsoluteImagePathCandidates(text) {
		rel, ok := normalizeHistoricalWorkspaceImagePath(candidate, c.workspaceRoot)
		if !ok || !isImageAttachmentRef(rel) {
			continue
		}
		if _, exists := seen[rel]; exists {
			continue
		}
		seen[rel] = struct{}{}
		image := ResolvedImage{Ref: "@" + rel, Path: rel}
		dataURL, err := visionFileImageDataURL(rel, c.workspaceRoot)
		if err != nil {
			image.Error = "image file unreadable or unsupported"
		} else {
			image.DataURL = dataURL
		}
		out = append(out, image)
	}
	return out
}

func historicalAbsoluteImagePathCandidates(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ' ', '\t', '\r', '\n', '"', '\'', '`', '<', '>', '(', ')', '[', ']', '{', '}', ',', ';', '!', '?':
			return true
		default:
			return false
		}
	})
	seen := map[string]struct{}{}
	var out []string
	for _, field := range fields {
		field = strings.TrimRight(field, ".，。；！？")
		if field == "" || (!filepath.IsAbs(field) && filepath.VolumeName(field) == "") || !isImageAttachmentRef(field) {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func normalizeHistoricalWorkspaceImagePath(path, workspaceRoot string) (string, bool) {
	path = strings.TrimSpace(path)
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if path == "" || workspaceRoot == "" || (!filepath.IsAbs(path) && filepath.VolumeName(path) == "") {
		return "", false
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(filepath.Clean(rel)), true
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
