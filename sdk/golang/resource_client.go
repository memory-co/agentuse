package agentuse

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
)

func (c *Client) PublishAgentVersion(ctx context.Context, user, id string, req PublishAgentVersionRequest) (*AgentVersion, error) {
	var v AgentVersion
	return &v, c.do(ctx, user, "POST", "/api/agents/"+esc(id)+"/versions", req, &v)
}
func (c *Client) ListAgentVersions(ctx context.Context, user, id string) ([]AgentVersion, error) {
	var vs []AgentVersion
	return vs, c.do(ctx, user, "GET", "/api/agents/"+esc(id)+"/versions", nil, &vs)
}
func (c *Client) GetAgentVersion(ctx context.Context, user, id, version string) (*AgentVersion, error) {
	var v AgentVersion
	return &v, c.do(ctx, user, "GET", "/api/agents/"+esc(id)+"/versions/"+esc(version), nil, &v)
}
func (c *Client) ListSkills(ctx context.Context, user string) ([]Skill, error) {
	var vs []Skill
	return vs, c.do(ctx, user, "GET", "/api/skills", nil, &vs)
}
func (c *Client) GetSkill(ctx context.Context, user, id string) (*Skill, error) {
	var v Skill
	return &v, c.do(ctx, user, "GET", "/api/skills/"+esc(id), nil, &v)
}
func (c *Client) ListSkillVersions(ctx context.Context, user, id string) ([]SkillVersion, error) {
	var vs []SkillVersion
	return vs, c.do(ctx, user, "GET", "/api/skills/"+esc(id)+"/versions", nil, &vs)
}
func (c *Client) GetSkillVersion(ctx context.Context, user, id, version string) (*SkillVersion, error) {
	var v SkillVersion
	return &v, c.do(ctx, user, "GET", "/api/skills/"+esc(id)+"/versions/"+esc(version), nil, &v)
}
func (c *Client) DeleteSkill(ctx context.Context, user, id string) error {
	return c.do(ctx, user, "DELETE", "/api/skills/"+esc(id), nil, nil)
}

// PublishSkill creates a skill when id is empty; otherwise base must be its current version.
func (c *Client) PublishSkill(ctx context.Context, user, id, base string, source io.Reader) (*Skill, error) {
	path := "/api/skills"
	if id != "" {
		path += "/" + esc(id) + "/versions"
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	mw := multipart.NewWriter(pw)
	go func() {
		err := mw.WriteField("base_version_id", base)
		if err == nil {
			var part io.Writer
			part, err = mw.CreateFormFile("file", "skill.zip")
			if err == nil {
				_, err = io.Copy(part, source)
			}
		}
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	response, err := c.attachmentRequest(ctx, user, "POST", path, mw.FormDataContentType(), pr)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var v Skill
	err = json.NewDecoder(response.Body).Decode(&v)
	return &v, err
}

// DownloadSkillPackage returns the immutable ZIP; the caller must close the stream.
func (c *Client) DownloadSkillPackage(ctx context.Context, user, id, version string) (io.ReadCloser, error) {
	response, err := c.attachmentRequest(ctx, user, "GET", "/api/skills/"+esc(id)+"/versions/"+esc(version)+"/package", "", nil)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// GetMessageContext asks Server to assemble the missing shared messages for the
// local session Client has selected. It does not create or submit native input.
func (c *Client) GetMessageContext(ctx context.Context, conversation, message, agent, localSession string) (*MessageContext, error) {
	var out MessageContext
	path := "/api/sessions/" + esc(conversation) + "/messages/" + esc(message) + "/agents/" + esc(agent) + "/context?client_session_id=" + esc(localSession)
	return &out, c.do(ctx, "", "GET", path, nil, &out)
}
