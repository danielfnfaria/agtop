package ui

import (
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/model"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// inView reports whether rows [from, from+height) of the content meet the visible window.
func inView(vp model.TreeViewport, from, height int) bool {
	if !vp.Scrolling {
		return true
	}
	return from+height > vp.Top && from < vp.Top+vp.ViewRows
}

// treeSpinning reports whether a spinner of the tree is on screen: a busy session box or an
// agent with an activity, in the visible window.
func treeSpinning(tm treeModel, vp model.TreeViewport) bool {
	for _, row := range tm.layout.Rows {
		for _, cl := range row.Clusters {
			s := tm.find(cl.Key)
			if s == nil {
				continue
			}
			if busy(s) && inView(vp, cl.Y, tm.layout.SessionHeight) {
				return true
			}
			for _, line := range cl.Lines {
				if !inView(vp, cl.Y+line.Y, model.AgentHeight) {
					continue
				}
				for _, k := range line.Keys {
					if a := s.Agents[k]; a != nil && domain.IsActive(a.Status) && a.Activity != nil {
						return true
					}
				}
			}
		}
	}
	return false
}

// drawTree draws the clusters on the body (rows [y, y+rows)): centered when everything fits,
// else the window of the viewport with the indicators above and below.
func (p painter) drawTree(c *canvas, y, _ int, tm treeModel, vp model.TreeViewport, selectedKey string, tones map[string]style.Color) {
	content := newCanvas(c.w, tm.layout.ContentHeight)
	muted := sty{fg: p.t.Muted}
	for _, row := range tm.layout.Rows {
		for _, cl := range row.Clusters {
			s := tm.find(cl.Key)
			if s == nil {
				continue
			}
			p.drawCluster(content, cl, s, cl.Key == selectedKey, tm.multiProfile, tones[s.ProfileLabel], muted)
		}
	}
	if !vp.Scrolling {
		c.blit(content, 0, content.h, y+vp.OffsetY)
		return
	}
	above, below := " ", " "
	if vp.Above {
		above = p.g.Up + " more above"
	}
	if vp.Below {
		below = p.g.Down + " more below"
	}
	c.line(0, y, c.w, p.g.Ellipsis, seg{above, muted})
	c.blit(content, vp.Top, vp.ViewRows, y+1)
	c.line(0, y+1+vp.ViewRows, c.w, p.g.Ellipsis, seg{below, muted})
}

func (p painter) drawCluster(c *canvas, cl model.ClusterLayout, s *store.SessionView, selected, showProfile bool, tone style.Color, muted sty) {
	x, y := cl.X, cl.Y
	p.sessionBox(c, x+cl.SessionLeft, y, s, selected, showProfile, tone)
	if cl.Stem != nil {
		c.text(x+cl.Stem.Left, y+cl.Stem.Y, seg{cl.Stem.Text, muted})
	}
	for _, line := range cl.Lines {
		c.text(x+line.Connector.Left, y+line.Connector.Y, seg{line.Connector.Text, muted})
		if line.HasTrunk {
			for r := range model.AgentHeight {
				c.text(x+line.Trunk, y+line.Y+r, seg{p.g.Line.Vertical, muted})
			}
		}
		for j, k := range line.Keys {
			if a := s.Agents[k]; a != nil {
				p.agentBox(c, x+line.Left+j*(model.AgentWidth+model.AgentGap), y+line.Y, a)
			}
		}
	}
}
