package v1

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	// TODO (user): Add any additional imports if needed
)

var _ = Describe("Pod Webhook", func() {
	var (
		obj       *corev1.Pod
		oldObj    *corev1.Pod
		defaulter PodCustomDefaulter
	)

	BeforeEach(func() {
		obj = &corev1.Pod{}
		oldObj = &corev1.Pod{}
		defaulter = PodCustomDefaulter{}
		Expect(defaulter).NotTo(BeNil(), "Expected defaulter to be initialized")
		Expect(oldObj).NotTo(BeNil(), "Expected oldObj to be initialized")
		Expect(obj).NotTo(BeNil(), "Expected obj to be initialized")
		// TODO (user): Add any setup logic common to all tests
	})

	AfterEach(func() {
		// TODO (user): Add any teardown logic common to all tests
	})

	Context("When creating Pod under Defaulting Webhook", func() {
		// TODO (user): Add logic for defaulting webhooks
		// Example:
		// It("Should apply defaults when a required field is empty", func() {
		//     By("simulating a scenario where defaults should be applied")
		//     obj.SomeFieldWithDefault = ""
		//     By("calling the Default method to apply defaults")
		//     defaulter.Default(ctx, obj)
		//     By("checking that the default values are set")
		//     Expect(obj.SomeFieldWithDefault).To(Equal("default_value"))
		// })
	})
})

var _ = Describe("compareAlternatives", func() {
	refs := func(alternatives []prioritizedAlternative) []string {
		result := make([]string, len(alternatives))
		for i, alt := range alternatives {
			result[i] = alt.reference
		}
		return result
	}

	Context("default behavior (all priorities 0)", func() {
		It("should preserve default type order: CISM < ISM < CRIS < RIS", func() {
			alternatives := []prioritizedAlternative{
				{reference: "ris-mirror", typeOrder: crTypeOrderRIS, declarationOrder: 0},
				{reference: "cism-mirror", typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "cris-upstream", typeOrder: crTypeOrderCRIS, declarationOrder: 0},
				{reference: "ism-mirror", typeOrder: crTypeOrderISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"cism-mirror", "ism-mirror", "cris-upstream", "ris-mirror",
			}))
		})

		It("should preserve YAML declaration order within same type", func() {
			alternatives := []prioritizedAlternative{
				{reference: "mirror-c", typeOrder: crTypeOrderCISM, declarationOrder: 2},
				{reference: "mirror-a", typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "mirror-b", typeOrder: crTypeOrderCISM, declarationOrder: 1},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"mirror-a", "mirror-b", "mirror-c",
			}))
		})
	})

	Context("CR priority", func() {
		It("should sort by CR priority ascending (negative first)", func() {
			alternatives := []prioritizedAlternative{
				{reference: "prio-0", crPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-5", crPriority: 5, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-neg10", crPriority: -10, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-neg1", crPriority: -1, typeOrder: crTypeOrderCISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"prio-neg10", "prio-neg1", "prio-0", "prio-5",
			}))
		})

		It("should fall back to type order on equal CR priority", func() {
			alternatives := []prioritizedAlternative{
				{reference: "ism", crPriority: -1, typeOrder: crTypeOrderISM, declarationOrder: 0},
				{reference: "cism", crPriority: -1, typeOrder: crTypeOrderCISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"cism", "ism",
			}))
		})
	})

	Context("intra-CR priority", func() {
		It("should sort positive intra-priorities ascending (lower = higher priority)", func() {
			alternatives := []prioritizedAlternative{
				{reference: "prio-30", intraPriority: 30, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-10", intraPriority: 10, typeOrder: crTypeOrderCISM, declarationOrder: 1},
				{reference: "prio-20", intraPriority: 20, typeOrder: crTypeOrderCISM, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"prio-10", "prio-20", "prio-30",
			}))
		})

		It("should place intra-priority 0 (default) before positive values", func() {
			alternatives := []prioritizedAlternative{
				{reference: "prio-5", intraPriority: 5, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "prio-1", intraPriority: 1, typeOrder: crTypeOrderCISM, declarationOrder: 1},
				{reference: "no-prio", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"no-prio", "prio-1", "prio-5",
			}))
		})

		It("should preserve YAML order among items with intra-priority 0", func() {
			alternatives := []prioritizedAlternative{
				{reference: "first", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 0},
				{reference: "second", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 1},
				{reference: "third", intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"first", "second", "third",
			}))
		})
	})

	Context("combined scenario from instructions.txt", func() {
		It("should order: ISM mirrors (prio -10) > CISM mirror (prio -1) > original (prio 0)", func() {
			alternatives := []prioritizedAlternative{
				{reference: "second", crPriority: -10, intraPriority: 5, typeOrder: crTypeOrderISM, declarationOrder: 0},
				{reference: "first", crPriority: -10, intraPriority: 1, typeOrder: crTypeOrderISM, declarationOrder: 1},
				{reference: "third", crPriority: -1, intraPriority: 0, typeOrder: crTypeOrderCISM, declarationOrder: 0},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"first", "second", "third",
			}))
		})

		It("should order ReplicatedImageSet upstreams by intra-priority", func() {
			alternatives := []prioritizedAlternative{
				{reference: "third", crPriority: 0, intraPriority: 30, typeOrder: crTypeOrderCRIS, declarationOrder: 0},
				{reference: "first", crPriority: 0, intraPriority: 10, typeOrder: crTypeOrderCRIS, declarationOrder: 1},
				{reference: "second", crPriority: 0, intraPriority: 20, typeOrder: crTypeOrderCRIS, declarationOrder: 2},
			}
			slices.SortStableFunc(alternatives, compareAlternatives)
			Expect(refs(alternatives)).To(Equal([]string{
				"first", "second", "third",
			}))
		})
	})
})
