package docker

import (
	"context"
	"fmt"
	"sync"
	"time"

	container_software "github.com/Yeatesss/container-software"
	"github.com/Yeatesss/container-software/core"
	"github.com/coocood/freecache"
	"github.com/dolthub/swiss"
	jsoniter "github.com/json-iterator/go"
	"github.com/weaveworks/scope/common/logger"
	"github.com/weaveworks/scope/report"
	"golang.org/x/sync/singleflight"
)

const MaxRetryAfter = 60 * 60 * 6 * time.Second

var softwareFinderSingle = singleflight.Group{}

type SoftwareFinder struct {
	CtrSofts    *freecache.Cache
	EnvPath     *freecache.Cache
	Labels      *freecache.Cache
	ContainerCh chan *FindContainer
}
type FindContainer struct {
	*core.Container
	retryAfter time.Duration
	suspect    bool
}

var SoftFinder = NewSoftwareFinder()

func NewSoftwareFinder() (finder *SoftwareFinder) {
	ctrCh := make(chan *FindContainer, 300)
	finder = &SoftwareFinder{
		ContainerCh: ctrCh,
		CtrSofts:    freecache.NewCache(10 * 1024 * 1024),
		EnvPath:     freecache.NewCache(5 * 1024 * 1024),
		Labels:      freecache.NewCache(5 * 1024 * 1024),
	}
	go func() {
		var works = sync.Map{}
		var concurrent = make(chan struct{}, 2)
		for ctr := range ctrCh {
			if _, ok := works.Load(ctr.Id); ok {
				continue
			}
			works.Store(ctr.Id, struct{}{})
			//fmt.Println(len(ctrCh))
			concurrent <- struct{}{}
			go func(container *FindContainer) {
				defer func() {
					<-concurrent
					works.Delete(container.Id)
				}()
				//logger.Logger.Debugf("Get Software: %s", container.Id)

				softwareFinderSingle.Do(container.Id, func() (interface{}, error) {
					webSoft, _ := finder.CtrSofts.Get([]byte(fmt.Sprintf("%s%s.%s", ctr.Id, ctr.Labels["master_pid"], "web")))
					dbSoft, _ := finder.CtrSofts.Get([]byte(fmt.Sprintf("%s%s.%s", ctr.Id, ctr.Labels["master_pid"], "database")))
					if len(dbSoft) > 0 || len(webSoft) > 0 {
						return nil, nil
					}

					var softMap = map[string]map[string]*core.Software{"web": make(map[string]*core.Software), "database": make(map[string]*core.Software)}
					ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second*time.Duration(container.Container.Processes.Len())*1*time.Second)
					defer cancel()
					logger.Logger.Debug("start collect software", "container_id", container.Id, "time", time.Now().Format("2006-01-02 15:04:05"))
					softWares, err := container_software.NewFinder().Find(ctx, container.Container)
					logger.Logger.Debug("finish collect software", "container_id", container.Id, "time", time.Now().Format("2006-01-02 15:04:05"))
					a, _ := jsoniter.MarshalToString(container)
					s, _ := jsoniter.MarshalToString(softWares)
					logger.Logger.Debugf("Find Software:%s,Data:%s\nsoft:%s", container.Id, a, s)

					if err != nil {
						logger.Logger.Errorf("Find ctr software error: %s,%v", container.Id, err)
						return nil, err
					}
					if len(softWares) == 0 {
						container.retryAfter = container.retryAfter * 2

						if container.suspect && container.retryAfter > 60*60*time.Second {
							container.retryAfter = 60 * 60 * time.Second
						}
						if !container.suspect && container.retryAfter > MaxRetryAfter {
							container.retryAfter = MaxRetryAfter
						}
						go func() {
							time.Sleep(container.retryAfter)
							logger.Logger.Debugf("Try again to get container applications: %s", container.Id)
							finder.ContainerCh <- container
						}()
						fmt.Println("写入成功", container.Id)
						logger.Logger.Debugf("Set Empty Software for container: %s", container.Id)

						finder.CtrSofts.Set([]byte(fmt.Sprintf("%s%s.%s", ctr.Id, ctr.Labels["master_pid"], "web")), []byte(`[]`), 0)
						return nil, nil
					}
					logger.Logger.Debugf("range container softwares: %s", container.Id)

					for _, ware := range softWares {
						if _, ok := softMap[string(ware.Type)]; !ok {
							continue
						}
						if co, ok := softMap[string(ware.Type)][ware.Name]; ok {
							co.BindEndpoint = append(co.BindEndpoint, ware.BindEndpoint...)
						} else {
							softMap[string(ware.Type)][ware.Name] = ware
						}
					}
					for idx, ware := range softMap {
						if ware != nil && len(ware) > 0 {
							logger.Logger.Debugf("set range container softwares: %s,%s", container.Id, container.Labels["master_pid"])

							var sets []string
							for _, software := range ware {
								s, _ := jsoniter.MarshalToString(software)
								sets = append(sets, s)
							}
							val, _ := jsoniter.Marshal(sets)
							key := fmt.Sprintf("%s%s.%s", container.Id, container.Labels["master_pid"], idx)
							err := finder.CtrSofts.Set([]byte(key), val, 0)
							if err != nil {
								return nil, err
							}
						}

					}
					return nil, err
				})
			}(ctr)

		}
	}()
	return
}
func (s *SoftwareFinder) ParseNodeSet(node report.Node, ctr *core.Container) report.Node {
	var hit bool
	for _, softType := range []string{"web", "database"} {
		if v, err := s.CtrSofts.Get([]byte(fmt.Sprintf("%s%s.%s", ctr.Id, ctr.Labels["master_pid"], softType))); err == nil {
			var softs []string
			logger.Logger.Debugf("Parse Software for container: %s, type: %s,data: %s", ctr.Id, softType, string(v))
			if err := jsoniter.Unmarshal(v, &softs); err == nil {
				hit = true
				if len(softs) > 0 {
					var sets report.StringSet
					for _, soft := range softs {
						sets = append(sets, soft)
					}
					node = node.WithSet("software."+softType, sets)
				}
			}
		}
	}
	if !hit {
		logger.Logger.Infof("Parse Software Not Hit:%s", ctr.Id)
		select {
		case s.ContainerCh <- &FindContainer{
			Container:  ctr,
			retryAfter: 20 * time.Second,
			suspect:    GetSuspectMap(ctr.Id),
		}:
		default:
			fmt.Println("阻塞", ctr.Id)
			//fmt.Println(len(s.ContainerCh))
		}

	}
	return node
}

type StrMapWrite struct {
	DataMap *swiss.Map[string, string]
	Lock    sync.RWMutex
}

func (m *StrMapWrite) Get(key string) (string, bool) {
	m.Lock.RLock()
	defer m.Lock.RUnlock()
	return m.DataMap.Get(key)
}

func (m *StrMapWrite) Put(key string, val string) {
	m.Lock.Lock()
	defer m.Lock.Unlock()
	m.DataMap.Put(key, val)
	return
}
