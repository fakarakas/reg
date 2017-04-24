package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"

	"time"

	"github.com/jessfraz/reg/clair"
	"github.com/jessfraz/reg/registry"
)

type registryController struct {
	reg *registry.Registry
	cl  *clair.Clair
}

type v1Compatibility struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
}

// A Repository holds data after a vulnerability scan of a single repo
type Repository struct {
	Name                string                    `json:"name"`
	Tag                 string                    `json:"tag"`
	Created             time.Time                 `json:"created"`
	URI                 string                    `json:"uri"`
	VulnerabilityReport clair.VulnerabilityReport `json:"vulnerability"`
}

// A AnalysisResult holds all vulnerabilities of a scan
type AnalysisResult struct {
	Repositories   []Repository `json:"repositories"`
	RegistryDomain string       `json:"registrydomain"`
	Name           string       `json:"name"`
}

func (rc *registryController) repositoriesHandler(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{
		"func":   "repositories",
		"URL":    r.URL,
		"method": r.Method,
	}).Info("fetching repositories")

	result := AnalysisResult{}
	result.RegistryDomain = rc.reg.Domain

	repoList, err := rc.reg.Catalog("")
	if err != nil {
		log.WithFields(log.Fields{
			"func":   "repositories",
			"URL":    r.URL,
			"method": r.Method,
		}).Errorf("getting catalog failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for _, repo := range repoList {
		repoURI := fmt.Sprintf("%s/%s", rc.reg.Domain, repo)
		r := Repository{
			Name: repo,
			URI:  repoURI,
		}

		result.Repositories = append(result.Repositories, r)
	}

	if err := tmpl.ExecuteTemplate(w, "repositories", result); err != nil {
		log.WithFields(log.Fields{
			"func":   "repositories",
			"URL":    r.URL,
			"method": r.Method,
		}).Errorf("template rendering failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (rc *registryController) tagHandler(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{
		"func":   "tag",
		"URL":    r.URL,
		"method": r.Method,
	}).Info("fetching tag")

	vars := mux.Vars(r)
	repo := vars["repo"]
	tag := vars["tag"]

	if repo == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Empty repo")
		return
	}

	if tag == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Empty tag")
		return
	}

	fmt.Fprintf(w, "Repo: %s Tag: %s ", repo, tag)
	return
}

func (rc *registryController) tagsHandler(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{
		"func":   "tags",
		"URL":    r.URL,
		"method": r.Method,
	}).Info("fetching tags")

	vars := mux.Vars(r)
	repo := vars["repo"]
	if repo == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Empty repo")
		return
	}

	tags, err := rc.reg.Tags(repo)
	if err != nil {
		log.WithFields(log.Fields{
			"func":   "tags",
			"URL":    r.URL,
			"method": r.Method,
		}).Errorf("getting tags for %s failed: %v", repo, err)

		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "No tags found")
		return
	}

	result := AnalysisResult{}
	result.RegistryDomain = rc.reg.Domain
	result.Name = repo
	for _, tag := range tags {
		// get the manifest
		m1, err := rc.reg.ManifestV1(repo, tag)
		if err != nil {
			log.WithFields(log.Fields{
				"func":   "tags",
				"URL":    r.URL,
				"method": r.Method,
			}).Errorf("getting v1 manifest for %s:%s failed: %v", repo, tag, err)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "Manifest not found")
			return
		}

		var createdDate time.Time
		for _, h := range m1.History {
			var comp v1Compatibility

			if err := json.Unmarshal([]byte(h.V1Compatibility), &comp); err != nil {
				log.WithFields(log.Fields{
					"func":   "tags",
					"URL":    r.URL,
					"method": r.Method,
				}).Errorf("unmarshal v1 manifest for %s:%s failed: %v", repo, tag, err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			createdDate = comp.Created
			break
		}

		repoURI := fmt.Sprintf("%s/%s", rc.reg.Domain, repo)
		if tag != "latest" {
			repoURI += ":" + tag
		}
		rp := Repository{
			Name:    repo,
			Tag:     tag,
			URI:     repoURI,
			Created: createdDate,
		}

		if rc.cl != nil {
			vuln, err := rc.cl.Vulnerabilities(rc.reg, repo, tag, m1)
			if err != nil {
				log.WithFields(log.Fields{
					"func":   "tags",
					"URL":    r.URL,
					"method": r.Method,
				}).Errorf("vulnerability scanning for %s:%s failed: %v", repo, tag, err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			rp.VulnerabilityReport = vuln
		}

		result.Repositories = append(result.Repositories, rp)
	}

	if err := tmpl.ExecuteTemplate(w, "tags", result); err != nil {
		log.WithFields(log.Fields{
			"func":   "tags",
			"URL":    r.URL,
			"method": r.Method,
		}).Errorf("template rendering failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	return
}

func (rc *registryController) vulnerabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{
		"func":   "vulnerabilities",
		"URL":    r.URL,
		"method": r.Method,
	}).Info("fetching vulnerabilities")

	vars := mux.Vars(r)
	repo := vars["repo"]
	tag := vars["tag"]

	if repo == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Empty repo")
		return
	}

	if tag == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Empty tag")
		return
	}

	m1, err := rc.reg.ManifestV1(repo, tag)
	if err != nil {
		log.WithFields(log.Fields{
			"func":   "vulnerabilities",
			"URL":    r.URL,
			"method": r.Method,
		}).Errorf("getting v1 manifest for %s:%s failed: %v", repo, tag, err)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Manifest not found")
		return
	}

	for _, h := range m1.History {
		var comp v1Compatibility

		if err := json.Unmarshal([]byte(h.V1Compatibility), &comp); err != nil {
			log.WithFields(log.Fields{
				"func":   "vulnerabilities",
				"URL":    r.URL,
				"method": r.Method,
			}).Errorf("unmarshal v1 manifest for %s:%s failed: %v", repo, tag, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		break
	}

	result := clair.VulnerabilityReport{}

	if rc.cl != nil {
		result, err = rc.cl.Vulnerabilities(rc.reg, repo, tag, m1)
		if err != nil {
			log.WithFields(log.Fields{
				"func":   "vulnerabilities",
				"URL":    r.URL,
				"method": r.Method,
			}).Errorf("vulnerability scanning for %s:%s failed: %v", repo, tag, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	if r.Header.Get("Accept-Encoding") == "application/json" {
		js, err := json.Marshal(result)
		if err != nil {
			log.WithFields(log.Fields{
				"func":   "vulnerabilities",
				"URL":    r.URL,
				"method": r.Method,
			}).Errorf("json marshal failed: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(js)
		return
	}

	if err := tmpl.ExecuteTemplate(w, "vulns", result); err != nil {
		log.WithFields(log.Fields{
			"func":   "vulnerabilities",
			"URL":    r.URL,
			"method": r.Method,
		}).Errorf("template rendering failed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	return
}