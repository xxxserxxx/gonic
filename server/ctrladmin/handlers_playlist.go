package ctrladmin

import (
	"bufio"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/jinzhu/gorm"

	"go.senan.xyz/gonic/db"
	"go.senan.xyz/gonic/server/ctrlsubsonic/specid"
)

var (
	errPlaylistNoMatch = errors.New("couldn't match track")
)

func playlistParseLine(c *Controller, absPath string) (*specid.ID, error) {
	if strings.HasPrefix(absPath, "#") || strings.TrimSpace(absPath) == "" {
		return nil, nil
	}
	var track db.Track
	query := c.DB.Raw(`
		SELECT tracks.id FROM TRACKS
		JOIN albums ON tracks.album_id=albums.id
		WHERE (albums.root_dir || ? || albums.left_path || albums.right_path || ? || tracks.filename)=?`,
		string(os.PathSeparator), string(os.PathSeparator), absPath)
	err := query.First(&track).Error
	if err == nil {
		return &specid.ID{Type: specid.Track, Value: track.ID}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("while matching: %w", err)
	}

	var pe db.PodcastEpisode
	err = c.DB.Where("path=?", absPath).First(&pe).Error
	if err == nil {
		return &specid.ID{Type: specid.PodcastEpisode, Value: pe.ID}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("while matching: %w", err)
	}

	return nil, fmt.Errorf("%v: %w", err, errPlaylistNoMatch)
}

func playlistCheckContentType(contentType string) bool {
	switch ct := strings.ToLower(contentType); ct {
	case
		"audio/x-mpegurl",
		"audio/mpegurl",
		"application/x-mpegurl",
		"application/octet-stream":
		return true
	}
	return false
}

func playlistParseUpload(c *Controller, userID int, header *multipart.FileHeader) ([]string, bool) {
	file, err := header.Open()
	if err != nil {
		return []string{fmt.Sprintf("couldn't open file %q", header.Filename)}, false
	}
	playlistName := strings.TrimSuffix(header.Filename, ".m3u8")
	if playlistName == "" {
		return []string{fmt.Sprintf("invalid filename %q", header.Filename)}, false
	}
	contentType := header.Header.Get("Content-Type")
	if !playlistCheckContentType(contentType) {
		return []string{fmt.Sprintf("invalid content-type %q", contentType)}, false
	}
	var trackIDs []specid.ID
	var errors []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		trackID, err := playlistParseLine(c, scanner.Text())
		if err != nil {
			// trim length of error to not overflow cookie flash
			errors = append(errors, fmt.Sprintf("%.100s", err.Error()))
			continue
		}
		if trackID.Value != 0 {
			trackIDs = append(trackIDs, *trackID)
		}
	}
	if err := scanner.Err(); err != nil {
		return []string{fmt.Sprintf("iterating playlist file: %v", err)}, true
	}
	playlist := &db.Playlist{}
	c.DB.FirstOrCreate(playlist, db.Playlist{
		Name:   playlistName,
		UserID: userID,
	})
	playlist.SetItems(trackIDs)
	c.DB.Save(playlist)
	return errors, true
}

func (c *Controller) ServeUploadPlaylist(r *http.Request) *Response {
	return &Response{template: "upload_playlist.tmpl"}
}

func (c *Controller) ServeUploadPlaylistDo(r *http.Request) *Response {
	if err := r.ParseMultipartForm((1 << 10) * 24); err != nil {
		return &Response{code: 500, err: "couldn't parse mutlipart"}
	}
	user := r.Context().Value(CtxUser).(*db.User)
	var playlistCount int
	var errors []string
	for _, headers := range r.MultipartForm.File {
		for _, header := range headers {
			headerErrors, created := playlistParseUpload(c, user.ID, header)
			if created {
				playlistCount++
			}
			errors = append(errors, headerErrors...)
		}
	}
	return &Response{
		redirect: "/admin/home",
		flashN:   []string{fmt.Sprintf("%d playlist(s) created", playlistCount)},
		flashW:   errors,
	}
}

func (c *Controller) ServeDeletePlaylistDo(r *http.Request) *Response {
	user := r.Context().Value(CtxUser).(*db.User)
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		return &Response{code: 400, err: "please provide a valid id"}
	}
	c.DB.
		Where("user_id=? AND id=?", user.ID, id).
		Delete(db.Playlist{})
	return &Response{
		redirect: "/admin/home",
	}
}
