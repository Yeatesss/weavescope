package app_test

import (
	"fmt"
	"github.com/weaveworks/scope/app"
	"sync"
	"testing"
	"time"
)

func TestK8sNode(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func(n int) {
			defer wg.Done()
			nodes := app.NodeReplenisher.GetNodes("111111")
			fmt.Printf("%d:%p\n", n, nodes)
			for ii := 0; ii < 10; ii++ {
				node := nodes.GetNode(fmt.Sprintf("abc-%d-%d", n, ii))
				//fmt.Println(fmt.Sprintf("abcc%d", i))
				node.NodeRole = fmt.Sprintf("role-1-%d", n)
			}
		}(i)
	}

	//go func() {
	//	defer wg.Done()
	//	nodes := app.NodeReplenisher.GetNodes("111111")
	//	fmt.Printf("2:%p\n", nodes)
	//	for i := 0; i < 10; i++ {
	//		node := nodes.GetNode(fmt.Sprintf("abcc%d", i))
	//		//fmt.Println(fmt.Sprintf("abcc%d", i))
	//		node.NodeRole = fmt.Sprintf("role-2-%d", i)
	//	}
	//	fmt.Println("finish 2222")
	//}()
	//
	//go func() {
	//	defer wg.Done()
	//	nodes := app.NodeReplenisher.GetNodes("222222")
	//	for i := 0; i < 10; i++ {
	//		node := nodes.GetNode(fmt.Sprintf("abc%d", i))
	//		//fmt.Println(fmt.Sprintf("abc%d", i))
	//		node.NodeRole = fmt.Sprintf("role-1-%d", i)
	//	}
	//	fmt.Println("finish 3333")
	//}()
	wg.Wait()
	//fmt.Println(app.NodeReplenisher.GetNodes("111111").nodes.Count())
	//fmt.Println(app.NodeReplenisher.GetNodes("222222").nodes.Count())

}
func TestK8sDeleteNode(t *testing.T) {
	nodes := app.NodeReplenisher.GetNodes("111111")
	for ii := 0; ii < 10; ii++ {
		node := nodes.GetNode(fmt.Sprintf("abc-%d", ii))
		//fmt.Println(fmt.Sprintf("abcc%d", i))
		node.NodeRole = fmt.Sprintf("role-1-%d", ii)
	}
	nodes.PreProcessed()
	for ii := 0; ii < 10; ii++ {
		if ii%2 == 0 {
			node := nodes.GetNode(fmt.Sprintf("abc-%d", ii))
			//fmt.Println(fmt.Sprintf("abcc%d", i))
			node.NodeRole = fmt.Sprintf("role-1-%d", ii)
			nodes.SetNode(fmt.Sprintf("abc-%d", ii), node)
		}

	}
	nodes.SetNode(fmt.Sprintf("abc-%d", 999), nodes.GetNode(fmt.Sprintf("abc-%d", 999)))

	app.NodeReplenisher.Clear <- "111111"
	time.Sleep(2 * time.Second)
	//fmt.Println(app.NodeReplenisher.GetNodes("111111").nodes.Count())
	//nodes.nodes.Iter(func(k string, v *app.Node) (stop bool) {
	//	fmt.Println(k, *v)
	//	return false
	//})
	//fmt.Println(app.NodeReplenisher.GetNodes("222222").nodes.Count())

}
func TestDeleteNode(t *testing.T) {
	nodes := app.NodeReplenisher.GetNodes("111111")
	nodes.SetNode("a", &app.Node{
		NodeRole: "abcabc",
	})
	nodes.SetNode("b", &app.Node{
		NodeRole: "abcabc",
	})
	nodes.SetNode("c", &app.Node{
		NodeRole: "abcabc",
	})
	fmt.Println(nodes.GetNode("a").IsDel())
	fmt.Println(nodes.GetNode("b").IsDel())
	fmt.Println(nodes.GetNode("c").IsDel())
	nodes.PreProcessed()
	fmt.Println(nodes.GetNode("a").IsDel())
	fmt.Println(nodes.GetNode("b").IsDel())
	fmt.Println(nodes.GetNode("c").IsDel())
	nodes.SetNode("a", nodes.GetNode("a"))
	fmt.Println(nodes.GetNode("a").IsDel())
	fmt.Println(nodes.GetNode("b").IsDel())
	fmt.Println(nodes.GetNode("c").IsDel())
	app.NodeReplenisher.Clear <- "111111"
	time.Sleep(3 * time.Second)
	//fmt.Println(nodes.nodes.Count())
	//nodes.nodes.Iter(func(k string, v *app.Node) (stop bool) {
	//	fmt.Println(k, *v)
	//	return false
	//})
	//fmt.Println(app.NodeReplenisher.GetNodes("111111").nodes.Count())

}
