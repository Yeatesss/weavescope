package app

import (
	"github.com/dolthub/swiss"
	"golang.org/x/sync/singleflight"
	"sync"
)

var NodeReplenisher = NewNodeReplenisher()

type NodeReplenish struct {
	sync.RWMutex
	sf    *singleflight.Group
	Clear chan string
	data  *swiss.Map[string, *Nodes]
}
type Node struct {
	isDelete       *bool
	name           string
	NodeRole       string
	RuntimeVersion string
}
type Nodes struct {
	sync.RWMutex
	sf         *singleflight.Group
	deleteMark *bool
	nodes      *swiss.Map[string, *Node]
}

func NewNodeReplenisher() *NodeReplenish {
	nr := &NodeReplenish{
		sf:    &singleflight.Group{},
		data:  swiss.NewMap[string, *Nodes](42),
		Clear: make(chan string, 50),
	}
	go func() {
		for clusterUUID := range nr.Clear {
			nodes := nr.GetNodes(clusterUUID)
			nodes.Lock()
			nodes.nodes.Iter(func(k string, v *Node) (stop bool) {
				if *v.isDelete {
					nodes.nodes.Delete(k)
				}
				return false
			})
			*nodes.deleteMark = false
			nodes.Unlock()

		}
	}()
	return nr
}

func NewNodes() *Nodes {
	return &Nodes{
		deleteMark: NewBoolPoint(false),
		sf:         &singleflight.Group{},
		nodes:      swiss.NewMap[string, *Node](42),
	}
}

/*
* GetNodes Get the set of nodes
 */
func (r *NodeReplenish) GetNodes(clusterUUID string) *Nodes {
	var (
		nodes, tmpNodes *Nodes
		ok              bool
	)
	for {
		//获取nodes
		r.RLock()
		tmpNodes, ok = r.data.Get(clusterUUID)
		r.RUnlock()
		if !ok {
			if nodes != nil {
				//If it does not currently exist and has been created, it returns directly
				r.Lock()
				r.data.Put(clusterUUID, nodes)
				r.Unlock()
				return nodes
			}
			//Create nodes
			nodesInterface, _, _ := r.sf.Do(clusterUUID, func() (interface{}, error) {
				return NewNodes(), nil
			})
			nodes = nodesInterface.(*Nodes)
			//Re-query once if it has been created and discard if it has been created
			continue
		}
		return tmpNodes
	}
}

/*
 * SetNode Set the node and assign the current deletemark to it
 */
func (r *Nodes) SetNode(nodeName string, n *Node) {
	r.Lock()
	n.isDelete = r.deleteMark
	n.name = nodeName
	r.nodes.Put(nodeName, n)
	r.Unlock()
	return
}

/*
 * GetNode There may be concurrency issues
 * but it doesn't matter that the data is not super concurrently updated on a single node
 */
func (r *Nodes) GetNode(nodeName string) *Node {

	r.RLock()
	n, ok := r.nodes.Get(nodeName)
	r.RUnlock()
	if !ok {
		r.Lock()
		//Get node by sigleflight, avoiding high concurrency that causes the same node to get different node points
		nodeInterface, _, _ := r.sf.Do(nodeName, func() (interface{}, error) {
			return &Node{name: nodeName, isDelete: r.deleteMark}, nil
		})
		n = nodeInterface.(*Node)
		r.nodes.Put(nodeName, n)
		r.Unlock()
	}
	return n
}

/*
 * PreProcessed Prepare the data for processing
 * mark all node information as deleted, and subsequently mark the node data as false by itself
 * and finally iterate through to delete the useless data
 */
func (r *Nodes) PreProcessed() {
	r.Lock()
	*r.deleteMark = true
	r.deleteMark = NewBoolPoint(false)
	r.Unlock()
	return
}
func (r *Node) IsDel() bool {
	return *r.isDelete
}
func (r *Node) NodeName() string {
	return r.name
}
func (r *Node) SetNodeRole(nodeRole string) *Node {
	r.NodeRole = nodeRole
	return r
}
func NewBoolPoint(boolean bool) *bool {
	f := boolean
	return &f
}
