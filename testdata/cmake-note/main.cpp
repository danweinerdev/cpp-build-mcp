void foo(int) {}
void foo(double) {}

int main() {
    struct Bar {};
    Bar b;
    foo(b);
    return 0;
}
