package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// Gallery window flag names (mvp/life-archive.md §7). Both bound the timeline
// inclusively against each media's logical_day — usable alone or together. The
// subject filter reuses the shared flagTo ("to") grammar the link/attach verbs
// already speak, so a browse names a subject the same way a link points at one.
const (
	galleryFlagSince = "since"
	galleryFlagUntil = "until"
)

// galleryEmpty is the calm copy printed when the window (or the whole store)
// holds no media to browse yet — an honest empty, not an error.
const galleryEmpty = "No media in view — attach a photo (or widen the window), then come back to browse it over time."

// galleryItemView is the --json projection of one timeline media: the full
// nine-field sidecar schema (data-model.md §"Sidecar schema") plus the derived
// stored_path the read surfaces. It mirrors attach's stored_path key and the
// MediaRecord field order so the wire shape reads like the spec; alt is a
// pointer so an absent alt renders as `alt: null`, and caption is always present
// (no omitempty) so the projection shape is stable for a harness.
type galleryItemView struct {
	ID               string  `json:"id"`
	SHA256           string  `json:"sha256"`
	OriginalFilename string  `json:"original_filename"`
	CapturedAt       string  `json:"captured_at"`
	LogicalDay       string  `json:"logical_day"`
	Caption          string  `json:"caption"`
	Alt              *string `json:"alt"`
	RawEntryID       string  `json:"raw_entry_id"`
	Source           string  `json:"source"`
	StoredPath       string  `json:"stored_path"`
}

// galleryView is the --json projection of a [router.GalleryResult]: the media in
// ascending logical_day order. Items is a non-nil slice (normalized to []) so a
// harness indexes it unconditionally; the read-only surface writes nothing, so
// there is no id or wrote flag.
type galleryView struct {
	Items []galleryItemView `json:"items"`
}

// newGalleryCmd wires `lucid gallery [--since] [--until] [--to <kind>:<key>]`:
// the read-only media-timeline surface (mvp/life-archive.md §7), the media
// sibling of recall. It projects the immutable media store and the append-only
// link ledger into a single date-ordered timeline for before/after
// progress-photo comparison — ascending by logical_day, filterable by an
// inclusive date window and/or one linked subject. It is strictly read-only:
// nothing under ~/.lucid/ changes and no model runs, mirroring the read-only
// `lucid recall` shape.
func newGalleryCmd() *cobra.Command {
	var since, until, to string
	cmd := &cobra.Command{
		Use:   "gallery",
		Short: "Read-only: browse stored media as a date-ordered timeline (never writes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			req := router.GalleryRequest{
				Since: since,
				Until: until,
				Now:   clockNow(),
			}
			// A --to token is parsed to (kind, key) up front; a malformed subject
			// is a fixed reason the user should read, so print it and exit
			// non-zero (the root silences returned errors).
			if strings.TrimSpace(to) != "" {
				kind, key, perr := router.ParseSubjectRef(to)
				if perr != nil {
					return emitErr(cmd, perr)
				}
				req.SubjectKind = kind
				req.SubjectKey = key
			}
			res, err := r.Gallery(req)
			if err != nil {
				// An unknown/unresolvable subject is refused by its native error;
				// surface it rather than a bare non-zero exit.
				return emitErr(cmd, err)
			}
			return renderGallery(cmd, res)
		},
	}
	f := cmd.Flags()
	f.StringVar(&since, galleryFlagSince, "", "Include only media on or after this logical day (YYYY-MM-DD, inclusive)")
	f.StringVar(&until, galleryFlagUntil, "", "Include only media on or before this logical day (YYYY-MM-DD, inclusive)")
	f.StringVar(&to, flagTo, "", "Browse only media linked to one subject, as <kind>:<key>")
	return cmd
}

// renderGallery prints the timeline: the --json projection, or Discord-friendly
// text (one bullet per media, no markdown table per the Discord output rules). An
// empty result prints the calm fallback.
func renderGallery(cmd *cobra.Command, res router.GalleryResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), galleryViewOf(res))
	}

	out := cmd.OutOrStdout()
	if !res.Found {
		_, _ = fmt.Fprintln(out, galleryEmpty)
		return nil
	}
	for _, it := range res.Items {
		_, _ = fmt.Fprintln(out, galleryLine(it.Media))
	}
	return nil
}

// galleryLine renders one media as a bullet: logical_day + caption + stored
// path, dropping the caption honestly when it is empty rather than printing an
// empty dash.
func galleryLine(m storage.MediaRecord) string {
	if strings.TrimSpace(m.Caption) != "" {
		return fmt.Sprintf("• %s — %s (%s)", m.LogicalDay, m.Caption, m.StoredPath)
	}
	return fmt.Sprintf("• %s (%s)", m.LogicalDay, m.StoredPath)
}

// galleryViewOf projects a router result into the stable --json shape,
// normalizing the nil slice to [] and carrying every sidecar field plus the
// derived stored_path.
func galleryViewOf(res router.GalleryResult) galleryView {
	view := galleryView{Items: make([]galleryItemView, 0, len(res.Items))}
	for _, it := range res.Items {
		m := it.Media
		view.Items = append(view.Items, galleryItemView{
			ID:               m.ID,
			SHA256:           m.SHA256,
			OriginalFilename: m.OriginalFilename,
			CapturedAt:       m.CapturedAt,
			LogicalDay:       m.LogicalDay,
			Caption:          m.Caption,
			Alt:              m.Alt,
			RawEntryID:       m.RawEntryID,
			Source:           m.Source,
			StoredPath:       m.StoredPath,
		})
	}
	return view
}
