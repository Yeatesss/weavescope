package cri

import (
	"context"
	"fmt"
	"runtime/pprof"
	"sync"
	"time"

	container_software "github.com/Yeatesss/container-software"
	"github.com/Yeatesss/container-software/core"
	"github.com/coocood/freecache"
	"github.com/dolthub/swiss"
	jsoniter "github.com/json-iterator/go"
	"github.com/weaveworks/scope/common/logger"
	"github.com/weaveworks/scope/report"
)

var SuspectMap = freecache.NewCache(1024 * 1024)

const MaxRetryAfter = 60 * 60 * 6 * time.Second

var ContainerPool = new(sync.Map)

type SoftwareFinder struct {
	CtrSofts    *freecache.Cache
	EnvPath     *freecache.Cache
	Labels      *freecache.Cache
	ContainerCh chan string
}
type FindContainer struct {
	*core.Container
	doing      bool
	skip       int  //需要重试时候+1
	active     bool //是否要执行软件清点
	retryAfter time.Duration
	suspect    bool
}

var SoftFinder = NewSoftwareFinder()

func NewSoftwareFinder() (finder *SoftwareFinder) {
	ctrCh := make(chan string, 300)
	var concurrent = make(chan struct{}, 2)

	finder = &SoftwareFinder{
		ContainerCh: ctrCh,
		CtrSofts:    freecache.NewCache(10 * 1024 * 1024),
		EnvPath:     freecache.NewCache(5 * 1024 * 1024),
		Labels:      freecache.NewCache(5 * 1024 * 1024),
	}
	go GuaranteedExecution(concurrent) //最终保底，保证肯定能执行
	go func() {
		//var works = sync.Map{}
		for ctr := range ctrCh {
			logger.Logger.Debugf("find container: %s", ctr)
			if findCtrInf, ok := ContainerPool.Load(ctr); ok {
				var finishChan = make(chan struct{}, 1)
				logger.Logger.Debugf("find container metadata: %s", ctr)

				if !findCtrInf.(*FindContainer).active {
					//如果容器未激活，则等待后重试，多次都未激活说明该容器已消失
					findCtr := findCtrInf.(*FindContainer)
					findCtr.skip++
					if findCtr.skip > 20 {
						return
					}
					time.Sleep(20 * time.Second)
					logger.Logger.Debugf("wait container software active: %s", findCtr.Id)
					finder.ContainerCh <- findCtr.Id
					ContainerPool.Store(findCtr.Id, findCtr)
					return
				}
				//容器已激活，可以进行软件清点
				findCtr := findCtrInf.(*FindContainer)
				findCtr.skip = 0
				findCtr.doing = true
				findCtr.active = false
				ContainerPool.Store(findCtr.Id, findCtr)
				concurrent <- struct{}{}
				logger.Logger.Debugf("new find container task: %s", ctr)
				go func(containerID string) {
					timer := time.NewTimer(900 * time.Second)
					defer timer.Stop()
					select {
					case <-finishChan:
					case <-timer.C:
						select {
						case <-concurrent:
							logger.Logger.Debugf("force container pop:%v", containerID)
						default:
							logger.Logger.Debug("force empty container pop")
						}
					}
				}(findCtr.Id)
				go func(container *FindContainer) {
					var removeCtrMap bool

					defer func() {
						finishChan <- struct{}{}
						close(finishChan)
						select {
						case <-concurrent:
							logger.Logger.Debugf("container pop:%s", container.Id)
						default:
							logger.Logger.Debugf("empty container pop:%s", container.Id)
						}
						logger.Logger.Debugf("finish find container task: %s,%v", container.Id, removeCtrMap)
						if !removeCtrMap {
							container.doing = false
							ContainerPool.Store(container.Id, container)
						}
					}()
					logger.Logger.Debugf("Get Software: %s", container.Id)

					webSoft, _ := finder.CtrSofts.Get([]byte(fmt.Sprintf("%s%s.%s", container.Id, container.Labels["master_pid"], "web")))
					dbSoft, _ := finder.CtrSofts.Get([]byte(fmt.Sprintf("%s%s.%s", container.Id, container.Labels["master_pid"], "database")))
					if len(dbSoft) > 0 || len(webSoft) > 0 {
						return
					}

					var softMap = map[string]map[string]*core.Software{"web": make(map[string]*core.Software), "database": make(map[string]*core.Software)}
					ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second+time.Duration(container.Container.Processes.Len())*1*time.Second)
					pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels("name", "FindContainer", "container_id", container.Id)))

					defer cancel()
					a, _ := jsoniter.MarshalToString(container)
					logger.Logger.Debug("start collect software", "container_id", container.Id, "container", a, "time", time.Now().Format("2006-01-02 15:04:05"))
					softWares, err := container_software.NewFinder().Find(ctx, container.Container)
					s, _ := jsoniter.MarshalToString(softWares)
					logger.Logger.Debug("finish collect software", "container_id", container.Id, "time", time.Now().Format("2006-01-02 15:04:05"), "software", s)

					if err != nil {
						logger.Logger.Errorf("Find ctr software error: %s,%v", container.Id, err)
						ContainerPool.Delete(container.Id)
						removeCtrMap = true
						return
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
							finder.ContainerCh <- container.Id
						}()
						fmt.Println("写入成功", container.Id)
						logger.Logger.Debugf("Set Empty Software for container: %s", container.Id)

						//finder.CtrSofts.Set([]byte(fmt.Sprintf("%s%s.%s", container.Id, container.Labels["master_pid"], "web")), []byte(`[]`), 0)
						return
					}
					logger.Logger.Debugf("range container softwares: %s", container.Id)

					for _, ware := range softWares {
						removeCtrMap = true
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
								logger.Logger.Errorf("Set Container software cache fail:%v", err)
								return
							}
						}

					}
					return
				}(findCtr)
			}

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
		logger.Logger.Debugf("Parse Software Not Hit:%s", ctr.Id)
		findCtr := &FindContainer{
			Container:  ctr,
			skip:       0,
			retryAfter: 20 * time.Second,
			suspect:    GetSuspectMap(ctr.Id),
		}
		ctrpool, ok := ContainerPool.Load(ctr.Id)
		if ok {
			//container map 存在容器信息，则更新信息
			findCtr.retryAfter = ctrpool.(*FindContainer).retryAfter
			findCtr.skip = ctrpool.(*FindContainer).skip
			if !ctrpool.(*FindContainer).doing {
				//如果容器未再执行更新激活状态
				logger.Logger.Debug("Update Get Software Container Metadata Active", "container_id", ctr.Id)
				findCtr.active = true
			}
			logger.Logger.Debug("Update Get Software Container Metadata", "container_id", ctr.Id)
			ContainerPool.Store(ctr.Id, findCtr)
		} else {
			findCtr.active = true
			ContainerPool.Store(ctr.Id, findCtr)
			logger.Logger.Debug("Add Get Software Container Metadata", "container_id", ctr.Id)

			select {
			case s.ContainerCh <- ctr.Id:
			default:
				fmt.Println("阻塞", ctr.Id)
				ContainerPool.Delete(ctr.Id)

				//fmt.Println(len(s.ContainerCh))
			}
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

func GetSuspectMap(containerID string) bool {
	exists, _ := SuspectMap.Get([]byte(containerID))
	return string(exists) == "1"
}

func GuaranteedExecution(concurrent chan struct{}) {
	var countTwo int64
	var countOne int64
	var clearLevelTwo int64 = 20
	var clearLevelOne int64 = 30
	for {
		if len(concurrent) == 2 {
			countTwo++
			countOne++
		} else if len(concurrent) == 1 {
			countOne++
			countTwo = 0
		} else {
			countTwo = 0
			countOne = 0
		}
		if countTwo > clearLevelTwo {
			countTwo = 0
			select {
			case <-concurrent:
				logger.Logger.Debug("container pop")
			default:
				logger.Logger.Debug("empty container pop")
			}
		}
		if countOne > clearLevelOne {
			countOne = 0
			select {
			case <-concurrent:
				logger.Logger.Debug("container pop")
			default:
				logger.Logger.Debug("empty container pop")
			}
		}
		time.Sleep(20 * time.Second)
	}
}
