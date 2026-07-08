package httpapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
)

func (s *Server) listSessions(c *gin.Context) {
	agentName := c.Param("name")
	namespace := c.DefaultQuery("namespace", defaultNamespace)
	limit := parseLimit(c, 100)
	continueToken := c.Query("continue")

	opts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{"agentscope.io/agent": agentName},
	}
	if limit > 0 {
		opts = append(opts, client.Limit(int64(limit)))
	}
	if continueToken != "" {
		opts = append(opts, client.Continue(continueToken))
	}

	var sessionList v1alpha1.AgentSessionList
	if err := s.client.List(c.Request.Context(), &sessionList, opts...); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	sessions := make([]SessionSummary, 0, len(sessionList.Items))
	for _, sess := range sessionList.Items {
		sessions = append(sessions, SessionSummary{
			ID:           sess.Name,
			AgentName:    agentName,
			Phase:        string(sess.Status.Phase),
			StartedAt:    sess.Status.StartedAt,
			LastActiveAt: sess.Status.LastActiveAt,
			MessageCount: sess.Status.MessageCount,
		})
	}

	resp := SessionListResponse{Sessions: sessions}
	if sessionList.Continue != "" {
		resp.Metadata = &ListMetadata{Continue: sessionList.Continue}
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) createSession(c *gin.Context) {
	agentName := c.Param("name")
	namespace := c.DefaultQuery("namespace", defaultNamespace)

	session := &v1alpha1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-sess-", agentName),
			Namespace:    namespace,
			Labels: map[string]string{
				"agentscope.io/agent": agentName,
			},
		},
		Spec: v1alpha1.AgentSessionSpec{
			AgentRef: v1alpha1.ObjectReference{Name: agentName},
		},
	}

	if err := s.client.Create(c.Request.Context(), session); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"sessionId": session.Name,
		"agentName": agentName,
		"phase":     "Active",
	})
}

func (s *Server) getSession(c *gin.Context) {
	sessionId := c.Param("sessionId")
	namespace := c.DefaultQuery("namespace", defaultNamespace)

	var session v1alpha1.AgentSession
	if err := s.client.Get(c.Request.Context(), types.NamespacedName{Name: sessionId, Namespace: namespace}, &session); err != nil {
		if errors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "session not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, session)
}

func (s *Server) getSessionState(c *gin.Context) {
	sessionId := c.Param("sessionId")
	namespace := c.DefaultQuery("namespace", defaultNamespace)

	var session v1alpha1.AgentSession
	if err := s.client.Get(c.Request.Context(), types.NamespacedName{Name: sessionId, Namespace: namespace}, &session); err != nil {
		if errors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "session not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if session.Status.State == nil {
		c.JSON(http.StatusOK, gin.H{"sessionId": sessionId, "state": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"sessionId": sessionId,
		"state":     session.Status.State,
	})
}

func (s *Server) compressSession(c *gin.Context) {
	sessionId := c.Param("sessionId")
	namespace := c.DefaultQuery("namespace", defaultNamespace)

	var session v1alpha1.AgentSession
	if err := s.client.Get(c.Request.Context(), types.NamespacedName{Name: sessionId, Namespace: namespace}, &session); err != nil {
		if errors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "session not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if session.Spec.Commands == nil {
		session.Spec.Commands = &v1alpha1.SessionCommands{}
	}
	session.Spec.Commands.Compress = true

	if err := s.client.Update(c.Request.Context(), &session); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessionId": sessionId, "command": "compress", "status": "initiated"})
}

func (s *Server) terminateSession(c *gin.Context) {
	sessionId := c.Param("sessionId")
	namespace := c.DefaultQuery("namespace", defaultNamespace)

	var session v1alpha1.AgentSession
	if err := s.client.Get(c.Request.Context(), types.NamespacedName{Name: sessionId, Namespace: namespace}, &session); err != nil {
		if errors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "session not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if session.Spec.Commands == nil {
		session.Spec.Commands = &v1alpha1.SessionCommands{}
	}
	session.Spec.Commands.Terminate = true

	if err := s.client.Update(c.Request.Context(), &session); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessionId": sessionId, "command": "terminate", "status": "initiated"})
}

func (s *Server) deleteSession(c *gin.Context) {
	sessionId := c.Param("sessionId")
	namespace := c.DefaultQuery("namespace", defaultNamespace)

	var session v1alpha1.AgentSession
	if err := s.client.Get(c.Request.Context(), types.NamespacedName{Name: sessionId, Namespace: namespace}, &session); err != nil {
		if errors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "session not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	if err := s.client.Delete(c.Request.Context(), &session); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}
