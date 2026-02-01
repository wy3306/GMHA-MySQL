package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/bbolt"
)

const (
	bucketClusters  = "clusters"
	bucketHosts     = "hosts"
	bucketInstances = "instances"
)

// Cluster 集群元数据
type Cluster struct {
	ID          string `json:"id"`
	WorkerAddr  string `json:"worker_addr"`
	CreatedAt   string `json:"created_at"`
}

// Host 主机纳管信息
type Host struct {
	ID       string `json:"id"`
	IP       string `json:"ip"`
	SSHUser  string `json:"ssh_user"`
	SSHPort  int    `json:"ssh_port"`
	ClusterID string `json:"cluster_id"`
}

// Instance MySQL 实例纳管信息
type Instance struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Role       string `json:"role"` // master / slave
	MasterAddr string `json:"master_addr,omitempty"`
	ClusterID  string `json:"cluster_id"`
}

// Store 本地存储
type Store struct {
	db   *bbolt.DB
	path string
}

// New 创建存储，path 为 bbolt 文件路径
func New(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, b := range []string{bucketClusters, bucketHosts, bucketInstances} {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// Close 关闭存储
func (s *Store) Close() error {
	return s.db.Close()
}

// AddCluster 添加集群
func (s *Store) AddCluster(c Cluster) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketClusters))
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return b.Put([]byte(c.ID), data)
	})
}

// ListClusters 列出所有集群
func (s *Store) ListClusters() ([]Cluster, error) {
	var list []Cluster
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketClusters))
		return b.ForEach(func(k, v []byte) error {
			var c Cluster
			if err := json.Unmarshal(v, &c); err != nil {
				return err
			}
			list = append(list, c)
			return nil
		})
	})
	return list, err
}

// GetCluster 获取单个集群
func (s *Store) GetCluster(id string) (*Cluster, error) {
	var c *Cluster
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketClusters))
		v := b.Get([]byte(id))
		if v == nil {
			return nil
		}
		c = &Cluster{}
		return json.Unmarshal(v, c)
	})
	return c, err
}

// AddHost 在集群下添加主机
func (s *Store) AddHost(clusterID string, h Host) error {
	h.ClusterID = clusterID
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketHosts))
		key := []byte(clusterID)
		var list []Host
		if v := b.Get(key); v != nil {
			_ = json.Unmarshal(v, &list)
		}
		h.ID = h.IP
		list = append(list, h)
		data, err := json.Marshal(list)
		if err != nil {
			return err
		}
		return b.Put(key, data)
	})
}

// ListHosts 列出集群下所有主机
func (s *Store) ListHosts(clusterID string) ([]Host, error) {
	var list []Host
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketHosts))
		v := b.Get([]byte(clusterID))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &list)
	})
	return list, err
}

// AddInstance 在集群下添加实例
func (s *Store) AddInstance(clusterID string, inst Instance) error {
	inst.ClusterID = clusterID
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketInstances))
		key := []byte(clusterID)
		var list []Instance
		if v := b.Get(key); v != nil {
			_ = json.Unmarshal(v, &list)
		}
		inst.ID = fmt.Sprintf("%s:%d", inst.Host, inst.Port)
		list = append(list, inst)
		data, err := json.Marshal(list)
		if err != nil {
			return err
		}
		return b.Put(key, data)
	})
}

// ListInstances 列出集群下所有实例
func (s *Store) ListInstances(clusterID string) ([]Instance, error) {
	var list []Instance
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketInstances))
		v := b.Get([]byte(clusterID))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, &list)
	})
	return list, err
}
