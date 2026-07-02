"""
二分搜索（Binary Search）实现
支持在有序数组中查找目标值，返回索引，找不到则返回 -1。
"""

from typing import List, Optional


def binary_search(arr: List[int], target: int) -> int:
    """
    迭代版二分搜索。
    时间复杂度 O(log n)，空间复杂度 O(1)。

    参数:
        arr: 升序排列的整数数组
        target: 要查找的目标值

    返回:
        目标值在数组中的索引，若不存在则返回 -1
    """
    lo, hi = 0, len(arr) - 1

    while lo <= hi:
        mid = lo + (hi - lo) // 2  # 避免 (lo+hi) 溢出

        if arr[mid] == target:
            return mid
        elif arr[mid] < target:
            lo = mid + 1
        else:
            hi = mid - 1

    return -1


def binary_search_recursive(arr: List[int], target: int,
                            lo: int = 0, hi: Optional[int] = None) -> int:
    """
    递归版二分搜索。
    时间复杂度 O(log n)，空间复杂度 O(log n)（调用栈）。

    参数:
        arr: 升序排列的整数数组
        target: 要查找的目标值
        lo: 搜索左边界（含），默认 0
        hi: 搜索右边界（含），默认 len(arr)-1

    返回:
        目标值在数组中的索引，若不存在则返回 -1
    """
    if hi is None:
        hi = len(arr) - 1

    if lo > hi:
        return -1

    mid = lo + (hi - lo) // 2

    if arr[mid] == target:
        return mid
    elif arr[mid] < target:
        return binary_search_recursive(arr, target, mid + 1, hi)
    else:
        return binary_search_recursive(arr, target, lo, mid - 1)


def lower_bound(arr: List[int], target: int) -> int:
    """
    查找第一个 >= target 的位置（下界）。
    即 bisect_left 语义。
    """
    lo, hi = 0, len(arr)

    while lo < hi:
        mid = lo + (hi - lo) // 2
        if arr[mid] < target:
            lo = mid + 1
        else:
            hi = mid

    return lo  # 可能等于 len(arr)，表示全部小于 target


def upper_bound(arr: List[int], target: int) -> int:
    """
    查找第一个 > target 的位置（上界）。
    即 bisect_right 语义。
    """
    lo, hi = 0, len(arr)

    while lo < hi:
        mid = lo + (hi - lo) // 2
        if arr[mid] <= target:
            lo = mid + 1
        else:
            hi = mid

    return lo


# ─── 测试 ───────────────────────────────────────────────
if __name__ == "__main__":
    test_arr = [1, 3, 4, 7, 9, 11, 15, 18]

    print(f"数组: {test_arr}")
    print()

    for val in [7, 1, 18, 5, 0, 20]:
        idx = binary_search(test_arr, val)
        idx_r = binary_search_recursive(test_arr, val)
        print(f"查找 {val:>2}:  迭代={idx:>2}, 递归={idx_r:>2}")

    print()
    print("下界 / 上界演示:")
    for val in [4, 5, 8]:
        lb = lower_bound(test_arr, val)
        ub = upper_bound(test_arr, val)
        print(f"  target={val}: lower_bound={lb}, upper_bound={ub}")
